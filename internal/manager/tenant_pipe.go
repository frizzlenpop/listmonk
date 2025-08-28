package manager

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/paulbellamy/ratecounter"
)

// tenantPipe handles campaign processing for a specific tenant with isolated context
type tenantPipe struct {
	tenantID   int
	camp       *models.Campaign
	rate       *ratecounter.RateCounter
	wg         *sync.WaitGroup
	sent       atomic.Int64
	lastID     atomic.Uint64
	errors     atomic.Uint64
	stopped    atomic.Bool
	withErrors atomic.Bool

	m *tenantInstanceManager
}

// newTenantPipe creates a new tenant-specific campaign pipe
func (tim *tenantInstanceManager) newTenantPipe(c *models.Campaign) (*tenantPipe, error) {
	// Validate messenger exists for this tenant
	if _, ok := tim.messengers[c.Messenger]; !ok {
		tim.store.UpdateTenantCampaignStatus(tim.tenantID, c.ID, models.CampaignStatusCancelled)
		return nil, fmt.Errorf("unknown messenger %s on campaign %s for tenant %d", c.Messenger, c.Name, tim.tenantID)
	}

	// Load the template with tenant-specific functions
	if err := c.CompileTemplate(tim.TemplateFuncs(c)); err != nil {
		return nil, err
	}

	// Load any media/attachments for the tenant
	if err := tim.attachMedia(c); err != nil {
		return nil, err
	}

	// Create tenant pipe
	tp := &tenantPipe{
		tenantID: tim.tenantID,
		camp:     c,
		rate:     ratecounter.NewRateCounter(time.Minute),
		wg:       &sync.WaitGroup{},
		m:        tim,
	}

	// Increment the waitgroup so that Wait() blocks immediately
	tp.wg.Add(1)

	go func() {
		// Wait for all messages in the campaign to be processed
		tp.wg.Wait()
		tp.cleanup()
	}()

	tim.pipesMut.Lock()
	tim.pipes[c.ID] = tp
	tim.pipesMut.Unlock()
	
	return tp, nil
}

// NextSubscribers processes the next batch of subscribers for this tenant's campaign
func (tp *tenantPipe) NextSubscribers() (bool, error) {
	// Fetch next batch of subscribers for this tenant and campaign
	subs, err := tp.m.store.NextTenantSubscribers(tp.tenantID, tp.camp.ID, tp.m.cfg.TenantMaxBatchSize)
	if err != nil {
		return false, fmt.Errorf("error fetching campaign subscribers for tenant %d (%s): %v", tp.tenantID, tp.camp.Name, err)
	}

	// No subscribers found for this tenant's campaign
	if len(subs) == 0 {
		return false, nil
	}

	// Check sliding window limits
	hasSliding := tp.m.cfg.SlidingWindow &&
		tp.m.cfg.SlidingWindowRate > 0 &&
		tp.m.cfg.SlidingWindowDuration.Seconds() > 1

	// Process messages with tenant context
	for _, s := range subs {
		msg, err := tp.newTenantMessage(s)
		if err != nil {
			tp.m.log.Printf("error rendering message for tenant %d (%s) (%s): %v", tp.tenantID, tp.camp.Name, s.Email, err)
			continue
		}

		// Push to tenant-specific message queue
		tp.m.campMsgQ <- msg

		// Apply sliding window limits per tenant
		if hasSliding {
			diff := time.Since(tp.m.slidingStart)

			if diff >= tp.m.cfg.SlidingWindowDuration {
				tp.m.slidingStart = time.Now()
				tp.m.slidingCount = 0
				continue
			}

			tp.m.slidingCount++
			if tp.m.slidingCount >= tp.m.cfg.SlidingWindowRate {
				wait := tp.m.cfg.SlidingWindowDuration - diff

				tp.m.log.Printf("tenant %d: messages exceeded (%d) for window (%v since %s). Sleeping for %s.",
					tp.tenantID,
					tp.m.slidingCount,
					tp.m.cfg.SlidingWindowDuration,
					tp.m.slidingStart.Format(time.RFC822Z),
					wait.Round(time.Second)*1)

				tp.m.slidingCount = 0
				time.Sleep(wait)
			}
		}
	}

	return true, nil
}

// OnError handles errors with tenant context
func (tp *tenantPipe) OnError() {
	if tp.m.cfg.TenantMaxSendErrors < 1 {
		return
	}

	count := tp.errors.Add(1)
	if int(count) < tp.m.cfg.TenantMaxSendErrors {
		return
	}

	tp.Stop(true)
	tp.m.log.Printf("tenant %d: error count exceeded %d. pausing campaign %s", 
		tp.tenantID, tp.m.cfg.TenantMaxSendErrors, tp.camp.Name)
}

// Stop marks a tenant campaign as stopped
func (tp *tenantPipe) Stop(withErrors bool) {
	if tp.stopped.Load() {
		return
	}

	if withErrors {
		tp.withErrors.Store(true)
	}

	tp.stopped.Store(true)
}

// newTenantMessage creates a tenant-specific campaign message
func (tp *tenantPipe) newTenantMessage(s models.Subscriber) (TenantCampaignMessage, error) {
	msg, err := tp.m.NewTenantCampaignMessage(tp.camp, s)
	if err != nil {
		return msg, err
	}

	msg.pipe = tp
	tp.wg.Add(1)

	return msg, nil
}

// cleanup finishes the tenant campaign and updates status with tenant context
func (tp *tenantPipe) cleanup() {
	defer func() {
		tp.m.pipesMut.Lock()
		delete(tp.m.pipes, tp.camp.ID)
		tp.m.pipesMut.Unlock()
	}()

	// Update campaign counts for this tenant
	if err := tp.m.store.UpdateTenantCampaignCounts(tp.tenantID, tp.camp.ID, 0, int(tp.sent.Load()), int(tp.lastID.Load())); err != nil {
		tp.m.log.Printf("tenant %d: error updating campaign counts (%s): %v", tp.tenantID, tp.camp.Name, err)
	}

	// Handle campaign paused due to errors
	if tp.withErrors.Load() {
		if err := tp.m.store.UpdateTenantCampaignStatus(tp.tenantID, tp.camp.ID, models.CampaignStatusPaused); err != nil {
			tp.m.log.Printf("tenant %d: error updating campaign (%s) status to %s: %v", 
				tp.tenantID, tp.camp.Name, models.CampaignStatusPaused, err)
		} else {
			tp.m.log.Printf("tenant %d: set campaign (%s) to %s", 
				tp.tenantID, tp.camp.Name, models.CampaignStatusPaused)
		}

		// Send tenant-specific notification
		_ = tp.m.sendTenantNotif(tp.camp, models.CampaignStatusPaused, "Too many errors")
		return
	}

	// Campaign was manually stopped
	if tp.stopped.Load() {
		tp.m.log.Printf("tenant %d: stop processing campaign (%s)", tp.tenantID, tp.camp.Name)
		return
	}

	// Campaign completed naturally - fetch updated status
	c, err := tp.m.store.GetTenantCampaign(tp.tenantID, tp.camp.ID)
	if err != nil {
		tp.m.log.Printf("tenant %d: error fetching campaign (%s) for ending: %v", tp.tenantID, tp.camp.Name, err)
		return
	}

	// Mark as finished if it was running
	if c.Status == models.CampaignStatusRunning || c.Status == models.CampaignStatusScheduled {
		c.Status = models.CampaignStatusFinished
		if err := tp.m.store.UpdateTenantCampaignStatus(tp.tenantID, tp.camp.ID, models.CampaignStatusFinished); err != nil {
			tp.m.log.Printf("tenant %d: error finishing campaign (%s): %v", tp.tenantID, tp.camp.Name, err)
		} else {
			tp.m.log.Printf("tenant %d: campaign (%s) finished", tp.tenantID, tp.camp.Name)
		}
	} else {
		tp.m.log.Printf("tenant %d: finish processing campaign (%s)", tp.tenantID, tp.camp.Name)
	}

	// Send tenant-specific notification
	_ = tp.m.sendTenantNotif(c, c.Status, "")
}