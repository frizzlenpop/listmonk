package manager

import (
	"bytes"
	"fmt"
	"html/template"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"maps"

	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// tenantInstanceManager methods

// AddMessenger adds a messenger to this tenant instance
func (tim *tenantInstanceManager) AddMessenger(msg Messenger) error {
	id := msg.Name()
	if _, ok := tim.messengers[id]; ok {
		return fmt.Errorf("messenger '%s' is already loaded for tenant %d", id, tim.tenantID)
	}
	tim.messengers[id] = msg
	return nil
}

// IsActive checks if this tenant instance is active
func (tim *tenantInstanceManager) IsActive() bool {
	tim.activeMut.RLock()
	defer tim.activeMut.RUnlock()
	return tim.active
}

// HasRunningCampaigns checks if this tenant has active campaigns
func (tim *tenantInstanceManager) HasRunningCampaigns() bool {
	tim.pipesMut.Lock()
	defer tim.pipesMut.Unlock()
	return len(tim.pipes) > 0
}

// GetCampaignStats returns campaign stats for this tenant
func (tim *tenantInstanceManager) GetCampaignStats(id int) CampStats {
	tim.pipesMut.Lock()
	defer tim.pipesMut.Unlock()

	if p, ok := tim.pipes[id]; ok {
		return CampStats{SendRate: int(p.rate.Rate())}
	}
	return CampStats{SendRate: 0}
}

// StopCampaign stops a campaign for this tenant
func (tim *tenantInstanceManager) StopCampaign(id int) {
	tim.pipesMut.RLock()
	defer tim.pipesMut.RUnlock()

	if p, ok := tim.pipes[id]; ok {
		p.Stop(false)
	}
}

// CacheTpl caches a template for this tenant
func (tim *tenantInstanceManager) CacheTpl(id int, tpl *models.Template) {
	tim.tplsMut.Lock()
	tim.tpls[id] = tpl
	tim.tplsMut.Unlock()
}

// DeleteTpl deletes a cached template for this tenant
func (tim *tenantInstanceManager) DeleteTpl(id int) {
	tim.tplsMut.Lock()
	delete(tim.tpls, id)
	tim.tplsMut.Unlock()
}

// GetTpl returns a cached template for this tenant
func (tim *tenantInstanceManager) GetTpl(id int) (*models.Template, error) {
	tim.tplsMut.RLock()
	tpl, ok := tim.tpls[id]
	tim.tplsMut.RUnlock()

	if !ok {
		return nil, fmt.Errorf("template %d not found for tenant %d", id, tim.tenantID)
	}
	return tpl, nil
}

// run starts the tenant instance processing loop
func (tim *tenantInstanceManager) run() {
	defer tim.wg.Done()

	// Start workers for this tenant
	for i := 0; i < tim.cfg.TenantMaxConcurrency; i++ {
		tim.wg.Add(1)
		go tim.worker()
	}

	// Start campaign scanning for this tenant
	if tim.cfg.ScanCampaigns {
		tim.wg.Add(1)
		go tim.scanCampaigns(tim.cfg.ScanInterval)
	}

	// Process tenant-specific pipes
	for {
		select {
		case tp := <-tim.nextPipes:
			has, err := tp.NextSubscribers()
			if err != nil {
				tim.log.Printf("tenant %d: error processing campaign batch (%s): %v", tim.tenantID, tp.camp.Name, err)
				continue
			}

			if has {
				// Queue for next batch
				select {
				case tim.nextPipes <- tp:
				default:
				}
			} else {
				// Mark pipe as done
				tp.wg.Done()
			}

		case <-tim.stopCh:
			tim.log.Printf("tenant %d: stopping instance manager", tim.tenantID)
			return
		}
	}
}

// stop stops this tenant instance
func (tim *tenantInstanceManager) stop() {
	tim.activeMut.Lock()
	defer tim.activeMut.Unlock()

	if !tim.active {
		return
	}

	tim.active = false
	close(tim.stopCh)
	tim.wg.Wait()

	// Close channels
	close(tim.nextPipes)
	close(tim.campMsgQ)
	close(tim.msgQ)
}

// triggerCampaignScan triggers a campaign scan for this tenant
func (tim *tenantInstanceManager) triggerCampaignScan() {
	// This will be called by the main tenant manager to trigger scans
	// The actual scanning happens in scanCampaigns
}

// scanCampaigns scans for campaigns specific to this tenant
func (tim *tenantInstanceManager) scanCampaigns(tick time.Duration) {
	defer tim.wg.Done()

	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			ids, counts := tim.getCurrentCampaigns()
			campaigns, err := tim.store.NextTenantCampaigns(tim.tenantID, ids, counts)
			if err != nil {
				tim.log.Printf("tenant %d: error fetching campaigns: %v", tim.tenantID, err)
				continue
			}

			for _, c := range campaigns {
				tp, err := tim.newTenantPipe(c)
				if err != nil {
					tim.log.Printf("tenant %d: error processing campaign (%s): %v", tim.tenantID, c.Name, err)
					continue
				}
				tim.log.Printf("tenant %d: start processing campaign (%s)", tim.tenantID, c.Name)

				select {
				case tim.nextPipes <- tp:
				default:
				}
			}

		case <-tim.stopCh:
			return
		}
	}
}

// getCurrentCampaigns returns current campaigns and counts for this tenant
func (tim *tenantInstanceManager) getCurrentCampaigns() ([]int64, []int64) {
	tim.pipesMut.RLock()
	defer tim.pipesMut.RUnlock()

	var (
		ids    = make([]int64, 0, len(tim.pipes))
		counts = make([]int64, 0, len(tim.pipes))
	)

	for _, p := range tim.pipes {
		ids = append(ids, int64(p.camp.ID))
		counts = append(counts, p.sent.Load())
		p.sent.Store(0)
	}

	return ids, counts
}

// worker processes messages for this tenant
func (tim *tenantInstanceManager) worker() {
	defer tim.wg.Done()

	numMsg := 0
	for {
		select {
		case msg, ok := <-tim.campMsgQ:
			if !ok {
				return
			}

			// Check if campaign is stopped
			if msg.pipe != nil && msg.pipe.stopped.Load() {
				msg.pipe.wg.Done()
				continue
			}

			// Apply tenant rate limiting
			if numMsg >= tim.cfg.TenantMessageRate {
				time.Sleep(time.Second)
				numMsg = 0
			}
			numMsg++

			// Create outgoing message with tenant context
			out := models.Message{
				From:        msg.from,
				To:          []string{msg.to},
				Subject:     msg.subject,
				ContentType: msg.Campaign.ContentType,
				Body:        msg.body,
				AltBody:     msg.altBody,
				Subscriber:  msg.Subscriber,
				Campaign:    msg.Campaign,
				Attachments: msg.Campaign.Attachments,
			}

			h := textproto.MIMEHeader{}
			h.Set(models.EmailHeaderCampaignUUID, msg.Campaign.UUID)
			h.Set(models.EmailHeaderSubscriberUUID, msg.Subscriber.UUID)
			h.Set("X-Tenant-ID", fmt.Sprintf("%d", tim.tenantID))

			// Add List-Unsubscribe headers if enabled
			if tim.cfg.UnsubHeader {
				h.Set("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
				h.Set("List-Unsubscribe", `<`+msg.unsubURL+`>`)
			}

			// Add custom headers
			if len(msg.Campaign.Headers) > 0 {
				for _, set := range msg.Campaign.Headers {
					for hdr, val := range set {
						h.Add(hdr, val)
					}
				}
			}

			out.Headers = h

			// Send message using tenant messenger
			err := tim.messengers[msg.Campaign.Messenger].Push(out)
			if err != nil {
				tim.log.Printf("tenant %d: error sending message in campaign %s: subscriber %d: %v", 
					tim.tenantID, msg.Campaign.Name, msg.Subscriber.ID, err)
			}

			// Update pipe statistics
			if msg.pipe != nil {
				msg.pipe.wg.Done()

				if err != nil {
					msg.pipe.OnError()
				} else {
					id := uint64(msg.Subscriber.ID)
					if id > msg.pipe.lastID.Load() {
						msg.pipe.lastID.Store(uint64(msg.Subscriber.ID))
					}
					msg.pipe.rate.Incr(1)
					msg.pipe.sent.Add(1)
				}
			}

		case msg, ok := <-tim.msgQ:
			if !ok {
				return
			}

			// Push arbitrary message
			if err := tim.messengers[msg.Messenger].Push(msg); err != nil {
				tim.log.Printf("tenant %d: error sending message '%s': %v", tim.tenantID, msg.Subject, err)
			}

		case <-tim.stopCh:
			return
		}
	}
}

// NewTenantCampaignMessage creates a tenant-specific campaign message
func (tim *tenantInstanceManager) NewTenantCampaignMessage(c *models.Campaign, s models.Subscriber) (TenantCampaignMessage, error) {
	msg := TenantCampaignMessage{
		TenantID:   tim.tenantID,
		Campaign:   c,
		Subscriber: s,
		subject:    c.Subject,
		from:       tim.getFromEmail(c),
		to:         s.Email,
		unsubURL:   fmt.Sprintf(tim.cfg.TenantUnsubURL, c.UUID, s.UUID),
	}

	if err := msg.render(); err != nil {
		return msg, err
	}

	return msg, nil
}

// getFromEmail returns the appropriate from email for this tenant
func (tim *tenantInstanceManager) getFromEmail(c *models.Campaign) string {
	// Use campaign-specific from email if set
	if c.FromEmail != "" {
		return c.FromEmail
	}
	
	// Use tenant-specific from email if configured
	if tim.cfg.TenantFromEmail != "" {
		return tim.cfg.TenantFromEmail
	}
	
	// Fall back to global config
	return tim.cfg.FromEmail
}

// TemplateFuncs returns template functions for this tenant
func (tim *tenantInstanceManager) TemplateFuncs(c *models.Campaign) template.FuncMap {
	f := template.FuncMap{
		"TrackLink": func(url string, msg *TenantCampaignMessage) string {
			subUUID := msg.Subscriber.UUID
			if !tim.cfg.IndividualTracking {
				subUUID = dummyUUID
			}
			return tim.trackLink(url, msg.Campaign.UUID, subUUID)
		},
		"TrackView": func(msg *TenantCampaignMessage) template.HTML {
			subUUID := msg.Subscriber.UUID
			if !tim.cfg.IndividualTracking {
				subUUID = dummyUUID
			}
			return template.HTML(fmt.Sprintf(`<img src="%s" alt="" />`,
				fmt.Sprintf(tim.cfg.ViewTrackURL, msg.Campaign.UUID, subUUID)))
		},
		"UnsubscribeURL": func(msg *TenantCampaignMessage) string {
			return msg.unsubURL
		},
		"ManageURL": func(msg *TenantCampaignMessage) string {
			return msg.unsubURL + "?manage=true"
		},
		"OptinURL": func(msg *TenantCampaignMessage) string {
			return fmt.Sprintf(tim.cfg.TenantOptinURL, msg.Subscriber.UUID, "")
		},
		"MessageURL": func(msg *TenantCampaignMessage) string {
			return fmt.Sprintf(tim.cfg.TenantMessageURL, c.UUID, msg.Subscriber.UUID)
		},
		"ArchiveURL": func() string {
			return tim.cfg.TenantArchiveURL
		},
		"RootURL": func() string {
			return tim.cfg.TenantRootURL
		},
	}

	maps.Copy(f, tim.tplFuncs)
	return f
}

// trackLink creates a tenant-specific tracking link
func (tim *tenantInstanceManager) trackLink(url, campUUID, subUUID string) string {
	url = strings.ReplaceAll(url, "&amp;", "&")

	tim.linksMut.RLock()
	if uu, ok := tim.links[url]; ok {
		tim.linksMut.RUnlock()
		return fmt.Sprintf(tim.cfg.LinkTrackURL, uu, campUUID, subUUID)
	}
	tim.linksMut.RUnlock()

	// Register link with tenant context
	uu, err := tim.store.CreateTenantLink(tim.tenantID, url)
	if err != nil {
		tim.log.Printf("tenant %d: error registering tracking for link '%s': %v", tim.tenantID, url, err)
		return url
	}

	tim.linksMut.Lock()
	tim.links[url] = uu
	tim.linksMut.Unlock()

	return fmt.Sprintf(tim.cfg.LinkTrackURL, uu, campUUID, subUUID)
}

// sendTenantNotif sends a tenant-specific notification
func (tim *tenantInstanceManager) sendTenantNotif(c *models.Campaign, status, reason string) error {
	subject := fmt.Sprintf("Tenant %d - %s: %s", tim.tenantID, cases.Title(language.Und).String(status), c.Name)
	data := map[string]any{
		"TenantID": tim.tenantID,
		"ID":       c.ID,
		"Name":     c.Name,
		"Status":   status,
		"Sent":     c.Sent,
		"ToSend":   c.ToSend,
		"Reason":   reason,
	}

	return tim.fnNotify(tim.tenantID, subject, data)
}

// attachMedia loads media/attachments for tenant campaigns
func (tim *tenantInstanceManager) attachMedia(c *models.Campaign) error {
	for _, mid := range []int64(c.MediaIDs) {
		a, err := tim.store.GetAttachment(int(mid))
		if err != nil {
			return fmt.Errorf("tenant %d: error fetching attachment %d on campaign %s: %v", tim.tenantID, mid, c.Name, err)
		}
		c.Attachments = append(c.Attachments, a)
	}
	return nil
}

// TenantCampaignMessage render method
func (m *TenantCampaignMessage) render() error {
	out := bytes.Buffer{}

	// Render subject if it's a template
	if m.Campaign.SubjectTpl != nil {
		if err := m.Campaign.SubjectTpl.ExecuteTemplate(&out, models.ContentTpl, m); err != nil {
			return err
		}
		m.subject = out.String()
		out.Reset()
	}

	// Compile main template
	if err := m.Campaign.Tpl.ExecuteTemplate(&out, models.BaseTpl, m); err != nil {
		return err
	}
	m.body = out.Bytes()

	// Handle alt body
	if m.Campaign.ContentType != models.CampaignContentTypePlain && m.Campaign.AltBody.Valid {
		if m.Campaign.AltBodyTpl != nil {
			b := bytes.Buffer{}
			if err := m.Campaign.AltBodyTpl.ExecuteTemplate(&b, models.ContentTpl, m); err != nil {
				return err
			}
			m.altBody = b.Bytes()
		} else {
			m.altBody = []byte(m.Campaign.AltBody.String)
		}
	}

	return nil
}

// TenantCampaignMessage accessor methods
func (m *TenantCampaignMessage) Subject() string {
	return m.subject
}

func (m *TenantCampaignMessage) Body() []byte {
	out := make([]byte, len(m.body))
	copy(out, m.body)
	return out
}

func (m *TenantCampaignMessage) AltBody() []byte {
	out := make([]byte, len(m.altBody))
	copy(out, m.altBody)
	return out
}