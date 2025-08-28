package manager

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"maps"

	"github.com/Masterminds/sprig/v3"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const (
	// BaseTPL is the name of the base template.
	BaseTPL = "base"

	// ContentTpl is the name of the compiled message.
	ContentTpl = "content"

	dummyUUID = "00000000-0000-0000-0000-000000000000"
)

// Store represents a data backend, such as a database,
// that provides subscriber and campaign records.
type Store interface {
	NextCampaigns(currentIDs []int64, sentCounts []int64) ([]*models.Campaign, error)
	NextSubscribers(campID, limit int) ([]models.Subscriber, error)
	GetCampaign(campID int) (*models.Campaign, error)
	GetAttachment(mediaID int) (models.Attachment, error)
	UpdateCampaignStatus(campID int, status string) error
	UpdateCampaignCounts(campID int, toSend int, sent int, lastSubID int) error
	CreateLink(url string) (string, error)
	BlocklistSubscriber(id int64) error
	DeleteSubscriber(id int64) error
}

// TenantStore extends Store with tenant-aware operations for multi-tenant campaign processing.
type TenantStore interface {
	Store
	// NextTenantCampaigns retrieves active campaigns for a specific tenant
	NextTenantCampaigns(tenantID int, currentIDs []int64, sentCounts []int64) ([]*models.Campaign, error)
	// NextTenantSubscribers retrieves subscribers for a campaign within a tenant
	NextTenantSubscribers(tenantID, campID, limit int) ([]models.Subscriber, error)
	// GetTenantCampaign fetches a campaign from a specific tenant
	GetTenantCampaign(tenantID, campID int) (*models.Campaign, error)
	// GetTenantSettings retrieves tenant-specific settings (SMTP, etc.)
	GetTenantSettings(tenantID int) (map[string]interface{}, error)
	// UpdateTenantCampaignStatus updates a campaign status within a tenant
	UpdateTenantCampaignStatus(tenantID, campID int, status string) error
	// UpdateTenantCampaignCounts updates campaign counts for a tenant-specific campaign
	UpdateTenantCampaignCounts(tenantID, campID int, toSend int, sent int, lastSubID int) error
	// CreateTenantLink creates a tracking link for a tenant
	CreateTenantLink(tenantID int, url string) (string, error)
	// BlocklistTenantSubscriber blocklists a subscriber within a tenant
	BlocklistTenantSubscriber(tenantID int, id int64) error
	// DeleteTenantSubscriber deletes a subscriber within a tenant
	DeleteTenantSubscriber(tenantID int, id int64) error
}

// Messenger is an interface for a generic messaging backend,
// for instance, e-mail, SMS etc.
type Messenger interface {
	Name() string
	Push(models.Message) error
	Flush() error
	Close() error
}

// CampStats contains campaign stats like per minute send rate.
type CampStats struct {
	SendRate int
}

// Manager handles the scheduling, processing, and queuing of campaigns
// and message pushes.
type Manager struct {
	cfg        Config
	store      Store
	i18n       *i18n.I18n
	messengers map[string]Messenger
	fnNotify   func(subject string, data any) error
	log        *log.Logger

	// Campaigns that are currently running.
	pipes    map[int]*pipe
	pipesMut sync.RWMutex

	tpls    map[int]*models.Template
	tplsMut sync.RWMutex

	// Links generated using Track() are cached here so as to not query
	// the database for the link UUID for every message sent. This has to
	// be locked as it may be used externally when previewing campaigns.
	links    map[string]string
	linksMut sync.RWMutex

	nextPipes chan *pipe
	campMsgQ  chan CampaignMessage
	msgQ      chan models.Message

	// Sliding window keeps track of the total number of messages sent in a period
	// and on reaching the specified limit, waits until the window is over before
	// sending further messages.
	slidingCount int
	slidingStart time.Time

	tplFuncs template.FuncMap
}

// TenantManager handles multi-tenant campaign processing with isolated
// per-tenant job queues and configurations.
type TenantManager struct {
	cfg           Config
	tenantStore   TenantStore
	i18n          *i18n.I18n
	fnNotify      func(tenantID int, subject string, data any) error
	log           *log.Logger

	// Per-tenant managers for isolated processing
	tenantManagers    map[int]*tenantInstanceManager
	tenantManagersMut sync.RWMutex

	// Global template functions
	tplFuncs template.FuncMap

	// Tenant discovery and lifecycle management
	activeTenants    map[int]bool
	activeTenantsMut sync.RWMutex

	// Control channels
	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

// tenantInstanceManager handles campaign processing for a single tenant
type tenantInstanceManager struct {
	tenantID   int
	cfg        TenantConfig
	store      TenantStore
	messengers map[string]Messenger
	i18n       *i18n.I18n
	fnNotify   func(tenantID int, subject string, data any) error
	log        *log.Logger

	// Tenant-specific processing state
	pipes    map[int]*tenantPipe
	pipesMut sync.RWMutex

	tpls    map[int]*models.Template
	tplsMut sync.RWMutex

	links    map[string]string
	linksMut sync.RWMutex

	// Tenant-specific processing queues
	nextPipes chan *tenantPipe
	campMsgQ  chan TenantCampaignMessage
	msgQ      chan models.Message

	// Tenant-specific rate limiting
	slidingCount int
	slidingStart time.Time

	// Lifecycle management
	active    bool
	activeMut sync.RWMutex
	stopCh    chan struct{}
	wg        sync.WaitGroup

	tplFuncs template.FuncMap
}

// TenantConfig extends Config with tenant-specific settings
type TenantConfig struct {
	Config
	TenantID int
	
	// Tenant-specific SMTP settings loaded from tenant_settings
	TenantFromEmail      string
	TenantSMTPHost       string
	TenantSMTPPort       int
	TenantSMTPUsername   string
	TenantSMTPPassword   string
	TenantSMTPTLS        bool
	TenantSMTPSkipVerify bool
	
	// Tenant-specific URLs and branding
	TenantRootURL     string
	TenantUnsubURL    string
	TenantOptinURL    string
	TenantMessageURL  string
	TenantArchiveURL  string
	
	// Tenant-specific limits and features
	TenantMaxBatchSize     int
	TenantMaxConcurrency   int
	TenantMessageRate      int
	TenantMaxSendErrors    int
}

// CampaignMessage represents an instance of campaign message to be pushed out,
// specific to a subscriber, via the campaign's messenger.
type CampaignMessage struct {
	Campaign   *models.Campaign
	Subscriber models.Subscriber

	from     string
	to       string
	subject  string
	body     []byte
	altBody  []byte
	unsubURL string

	pipe *pipe
}

// TenantCampaignMessage extends CampaignMessage with tenant context
type TenantCampaignMessage struct {
	TenantID   int
	Campaign   *models.Campaign
	Subscriber models.Subscriber

	from     string
	to       string
	subject  string
	body     []byte
	altBody  []byte
	unsubURL string

	pipe *tenantPipe
}

// Config has parameters for configuring the manager.
type Config struct {
	// Number of subscribers to pull from the DB in a single iteration.
	BatchSize             int
	Concurrency           int
	MessageRate           int
	MaxSendErrors         int
	SlidingWindow         bool
	SlidingWindowDuration time.Duration
	SlidingWindowRate     int
	RequeueOnError        bool
	FromEmail             string
	IndividualTracking    bool
	LinkTrackURL          string
	UnsubURL              string
	OptinURL              string
	MessageURL            string
	ViewTrackURL          string
	ArchiveURL            string
	RootURL               string
	UnsubHeader           bool

	// Interval to scan the DB for active campaign checkpoints.
	ScanInterval time.Duration

	// ScanCampaigns indicates whether this instance of manager will scan the DB
	// for active campaigns and process them.
	// This can be used to run multiple instances of listmonk
	// (exposed to the internet, private etc.) where only one does campaign
	// processing while the others handle other kinds of traffic.
	ScanCampaigns bool
}

var pushTimeout = time.Second * 3

// NewTenantManager returns a new instance of multi-tenant Manager.
func NewTenantManager(cfg Config, store TenantStore, i *i18n.I18n, l *log.Logger) *TenantManager {
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 1000
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.MessageRate < 1 {
		cfg.MessageRate = 1
	}

	tm := &TenantManager{
		cfg:            cfg,
		tenantStore:    store,
		i18n:           i,
		log:            l,
		tenantManagers: make(map[int]*tenantInstanceManager),
		activeTenants:  make(map[int]bool),
		shutdownCh:     make(chan struct{}),
		fnNotify: func(tenantID int, subject string, data any) error {
			return notifs.NotifySystem(subject, notifs.TplCampaignStatus, data, nil)
		},
	}
	tm.tplFuncs = tm.makeGenericFuncMap()

	return tm
}

// New returns a new instance of Mailer.
// This function maintains backward compatibility for single-tenant deployments.
func New(cfg Config, store Store, i *i18n.I18n, l *log.Logger) *Manager {
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 1000
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.MessageRate < 1 {
		cfg.MessageRate = 1
	}

	m := &Manager{
		cfg:   cfg,
		store: store,
		i18n:  i,
		fnNotify: func(subject string, data any) error {
			return notifs.NotifySystem(subject, notifs.TplCampaignStatus, data, nil)
		},
		log:          l,
		messengers:   make(map[string]Messenger),
		pipes:        make(map[int]*pipe),
		tpls:         make(map[int]*models.Template),
		links:        make(map[string]string),
		nextPipes:    make(chan *pipe, 1000),
		campMsgQ:     make(chan CampaignMessage, cfg.Concurrency*cfg.MessageRate*2),
		msgQ:         make(chan models.Message, cfg.Concurrency*cfg.MessageRate*2),
		slidingStart: time.Now(),
	}
	m.tplFuncs = m.makeGnericFuncMap()

	l.Printf("initialized single-tenant campaign manager (legacy mode)")
	return m
}

// NewFromTenantStore creates a Manager that uses a TenantStore but operates in single-tenant mode.
// This provides backward compatibility while using the new tenant-aware store interface.
func NewFromTenantStore(cfg Config, store TenantStore, i *i18n.I18n, l *log.Logger) *Manager {
	// Wrap the TenantStore to make it compatible with the legacy Store interface
	legacyStore := &tenantStoreAdapter{
		tenantStore: store,
		defaultTenantID: 1, // Use tenant ID 1 as default for backward compatibility
	}
	
	m := New(cfg, legacyStore, i, l)
	l.Printf("initialized single-tenant campaign manager with tenant store adapter")
	return m
}

// tenantStoreAdapter adapts a TenantStore to work with the legacy Store interface
// by defaulting to a specific tenant ID for all operations
type tenantStoreAdapter struct {
	tenantStore     TenantStore
	defaultTenantID int
}

// NextCampaigns adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) NextCampaigns(currentIDs []int64, sentCounts []int64) ([]*models.Campaign, error) {
	return tsa.tenantStore.NextTenantCampaigns(tsa.defaultTenantID, currentIDs, sentCounts)
}

// NextSubscribers adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) NextSubscribers(campID, limit int) ([]models.Subscriber, error) {
	return tsa.tenantStore.NextTenantSubscribers(tsa.defaultTenantID, campID, limit)
}

// GetCampaign adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) GetCampaign(campID int) (*models.Campaign, error) {
	return tsa.tenantStore.GetTenantCampaign(tsa.defaultTenantID, campID)
}

// GetAttachment uses the base store method
func (tsa *tenantStoreAdapter) GetAttachment(mediaID int) (models.Attachment, error) {
	return tsa.tenantStore.GetAttachment(mediaID)
}

// UpdateCampaignStatus adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) UpdateCampaignStatus(campID int, status string) error {
	return tsa.tenantStore.UpdateTenantCampaignStatus(tsa.defaultTenantID, campID, status)
}

// UpdateCampaignCounts adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) UpdateCampaignCounts(campID int, toSend int, sent int, lastSubID int) error {
	return tsa.tenantStore.UpdateTenantCampaignCounts(tsa.defaultTenantID, campID, toSend, sent, lastSubID)
}

// CreateLink adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) CreateLink(url string) (string, error) {
	return tsa.tenantStore.CreateTenantLink(tsa.defaultTenantID, url)
}

// BlocklistSubscriber adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) BlocklistSubscriber(id int64) error {
	return tsa.tenantStore.BlocklistTenantSubscriber(tsa.defaultTenantID, id)
}

// DeleteSubscriber adapts the tenant method to the legacy interface
func (tsa *tenantStoreAdapter) DeleteSubscriber(id int64) error {
	return tsa.tenantStore.DeleteTenantSubscriber(tsa.defaultTenantID, id)
}

// AddMessenger adds a Messenger messaging backend to the manager.
func (m *Manager) AddMessenger(msg Messenger) error {
	id := msg.Name()
	if _, ok := m.messengers[id]; ok {
		return fmt.Errorf("messenger '%s' is already loaded", id)
	}
	m.messengers[id] = msg

	return nil
}

// PushMessage pushes an arbitrary non-campaign Message to be sent out by the workers.
// It times out if the queue is busy.
func (m *Manager) PushMessage(msg models.Message) error {
	t := time.NewTicker(pushTimeout)
	defer t.Stop()

	select {
	case m.msgQ <- msg:
	case <-t.C:
		m.log.Printf("message push timed out: '%s'", msg.Subject)
		return errors.New("message push timed out")
	}

	return nil
}

// PushCampaignMessage pushes a campaign messages into a queue to be sent out by the workers.
// It times out if the queue is busy.
func (m *Manager) PushCampaignMessage(msg CampaignMessage) error {
	t := time.NewTicker(pushTimeout)
	defer t.Stop()

	// Load any media/attachments.
	if err := m.attachMedia(msg.Campaign); err != nil {
		return err
	}

	select {
	case m.campMsgQ <- msg:
	case <-t.C:
		m.log.Printf("message push timed out: '%s'", msg.Subject())
		return errors.New("message push timed out")
	}

	return nil
}

// HasMessenger checks if a given messenger is registered.
func (m *Manager) HasMessenger(id string) bool {
	_, ok := m.messengers[id]

	return ok
}

// HasRunningCampaigns checks if there are any active campaigns.
func (m *Manager) HasRunningCampaigns() bool {
	m.pipesMut.Lock()
	defer m.pipesMut.Unlock()

	return len(m.pipes) > 0
}

// GetCampaignStats returns campaign statistics.
func (m *Manager) GetCampaignStats(id int) CampStats {
	n := 0

	m.pipesMut.Lock()
	if c, ok := m.pipes[id]; ok {
		n = int(c.rate.Rate())
	}
	m.pipesMut.Unlock()

	return CampStats{SendRate: n}
}

// Run is a blocking function (that should be invoked as a goroutine)
// that scans the data source at regular intervals for pending campaigns,
// and queues them for processing. The process queue fetches batches of
// subscribers and pushes messages to them for each queued campaign
// until all subscribers are exhausted, at which point, a campaign is marked
// as "finished".
func (m *Manager) Run() {
	if m.cfg.ScanCampaigns {
		// Periodically scan campaigns and push running campaigns to nextPipes
		// to fetch subscribers from the campaign.
		go m.scanCampaigns(m.cfg.ScanInterval)
	}

	// Spawn N message workers.
	for i := 0; i < m.cfg.Concurrency; i++ {
		go m.worker()
	}

	// Indefinitely wait on the pipe queue to fetch the next set of subscribers
	// for any active campaigns.
	for p := range m.nextPipes {
		has, err := p.NextSubscribers()
		if err != nil {
			m.log.Printf("error processing campaign batch (%s): %v", p.camp.Name, err)
			continue
		}

		if has {
			// There are more subscribers to fetch. Queue again.
			select {
			case m.nextPipes <- p:
			default:
			}
		} else {
			// The pipe is created with a +1 on the waitgroup pseudo counter
			// so that it immediately waits. Subsequently, every message created
			// is incremented in the counter in pipe.newMessage(), and when it's'
			// processed (or ignored when a campaign is paused or cancelled),
			// the count is's reduced in worker().
			//
			// This marks down the original non-message +1, causing the waitgroup
			// to be released and the pipe to end, triggering the pg.Wait()
			// in newPipe() that calls pipe.cleanup().
			p.wg.Done()
		}
	}
}

// CacheTpl caches a template for ad-hoc use. This is currently only used by tx templates.
func (m *Manager) CacheTpl(id int, tpl *models.Template) {
	m.tplsMut.Lock()
	m.tpls[id] = tpl
	m.tplsMut.Unlock()
}

// DeleteTpl deletes a cached template.
func (m *Manager) DeleteTpl(id int) {
	m.tplsMut.Lock()
	delete(m.tpls, id)
	m.tplsMut.Unlock()
}

// GetTpl returns a cached template.
func (m *Manager) GetTpl(id int) (*models.Template, error) {
	m.tplsMut.RLock()
	tpl, ok := m.tpls[id]
	m.tplsMut.RUnlock()

	if !ok {
		return nil, fmt.Errorf("template %d not found", id)
	}

	return tpl, nil
}

// TemplateFuncs returns the template functions to be applied into
// compiled campaign templates.
func (m *Manager) TemplateFuncs(c *models.Campaign) template.FuncMap {
	f := template.FuncMap{
		"TrackLink": func(url string, msg *CampaignMessage) string {
			subUUID := msg.Subscriber.UUID
			if !m.cfg.IndividualTracking {
				subUUID = dummyUUID
			}

			return m.trackLink(url, msg.Campaign.UUID, subUUID)
		},
		"TrackView": func(msg *CampaignMessage) template.HTML {
			subUUID := msg.Subscriber.UUID
			if !m.cfg.IndividualTracking {
				subUUID = dummyUUID
			}

			return template.HTML(fmt.Sprintf(`<img src="%s" alt="" />`,
				fmt.Sprintf(m.cfg.ViewTrackURL, msg.Campaign.UUID, subUUID)))
		},
		"UnsubscribeURL": func(msg *CampaignMessage) string {
			return msg.unsubURL
		},
		"ManageURL": func(msg *CampaignMessage) string {
			return msg.unsubURL + "?manage=true"
		},
		"OptinURL": func(msg *CampaignMessage) string {
			// Add list IDs.
			// TODO: Show private lists list on optin e-mail
			return fmt.Sprintf(m.cfg.OptinURL, msg.Subscriber.UUID, "")
		},
		"MessageURL": func(msg *CampaignMessage) string {
			return fmt.Sprintf(m.cfg.MessageURL, c.UUID, msg.Subscriber.UUID)
		},
		"ArchiveURL": func() string {
			return m.cfg.ArchiveURL
		},
		"RootURL": func() string {
			return m.cfg.RootURL
		},
	}

	maps.Copy(f, m.tplFuncs)

	return f
}

func (m *Manager) GenericTemplateFuncs() template.FuncMap {
	return m.tplFuncs
}

// StopCampaign marks a running campaign as stopped so that all its queued messages are ignored.
func (m *Manager) StopCampaign(id int) {
	m.pipesMut.RLock()
	if p, ok := m.pipes[id]; ok {
		p.Stop(false)
	}
	m.pipesMut.RUnlock()
}

// Close closes and exits the campaign manager.
func (m *Manager) Close() {
	close(m.nextPipes)
	close(m.msgQ)
}

// TenantManager Methods

// AddMessenger adds a Messenger to all tenant instances.
func (tm *TenantManager) AddMessenger(msg Messenger) error {
	tm.tenantManagersMut.RLock()
	defer tm.tenantManagersMut.RUnlock()

	id := msg.Name()
	// Add to all existing tenant managers
	for _, t := range tm.tenantManagers {
		if err := t.AddMessenger(msg); err != nil {
			return fmt.Errorf("failed to add messenger to tenant %d: %v", t.tenantID, err)
		}
	}

	return nil
}

// Run starts the multi-tenant campaign processing.
func (tm *TenantManager) Run() {
	// Start tenant discovery and lifecycle management
	tm.wg.Add(1)
	go tm.manageTenants()

	// Start periodic tenant scanning
	if tm.cfg.ScanCampaigns {
		tm.wg.Add(1)
		go tm.scanActiveTenants(tm.cfg.ScanInterval)
	}

	// Wait for shutdown
	<-tm.shutdownCh
	tm.wg.Wait()
}

// Close closes the tenant manager and all tenant instances.
func (tm *TenantManager) Close() {
	close(tm.shutdownCh)

	// Stop all tenant instances
	tm.tenantManagersMut.Lock()
	for _, t := range tm.tenantManagers {
		t.stop()
	}
	tm.tenantManagersMut.Unlock()

	tm.wg.Wait()
}

// GetTenantCampaignStats returns campaign stats for a specific tenant.
func (tm *TenantManager) GetTenantCampaignStats(tenantID, campID int) CampStats {
	tm.tenantManagersMut.RLock()
	defer tm.tenantManagersMut.RUnlock()

	if t, exists := tm.tenantManagers[tenantID]; exists {
		return t.GetCampaignStats(campID)
	}
	return CampStats{SendRate: 0}
}

// HasRunningCampaigns checks if any tenant has active campaigns.
func (tm *TenantManager) HasRunningCampaigns() bool {
	tm.tenantManagersMut.RLock()
	defer tm.tenantManagersMut.RUnlock()

	for _, t := range tm.tenantManagers {
		if t.HasRunningCampaigns() {
			return true
		}
	}
	return false
}

// StopTenantCampaign stops a campaign for a specific tenant.
func (tm *TenantManager) StopTenantCampaign(tenantID, campID int) {
	tm.tenantManagersMut.RLock()
	defer tm.tenantManagersMut.RUnlock()

	if t, exists := tm.tenantManagers[tenantID]; exists {
		t.StopCampaign(campID)
	}
}

// manageTenants handles the discovery and lifecycle of tenant instances.
func (tm *TenantManager) manageTenants() {
	defer tm.wg.Done()

	// Discover active tenants periodically
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Initial tenant discovery
	tm.discoverActiveTenants()

	for {
		select {
		case <-ticker.C:
			tm.discoverActiveTenants()
		case <-tm.shutdownCh:
			return
		}
	}
}

// discoverActiveTenants finds active tenants and creates instances.
func (tm *TenantManager) discoverActiveTenants() {
	// Get list of active tenants from database
	// This would need to be implemented in the TenantStore
	tenantIDs, err := tm.getActiveTenantIDs()
	if err != nil {
		tm.log.Printf("error discovering active tenants: %v", err)
		return
	}

	tm.activeTenantsMut.Lock()
	tm.tenantManagersMut.Lock()
	defer tm.activeTenantsMut.Unlock()
	defer tm.tenantManagersMut.Unlock()

	// Add new tenants
	for _, tenantID := range tenantIDs {
		if !tm.activeTenants[tenantID] {
			if err := tm.createTenantInstance(tenantID); err != nil {
				tm.log.Printf("error creating tenant instance %d: %v", tenantID, err)
				continue
			}
			tm.activeTenants[tenantID] = true
			tm.log.Printf("created tenant manager instance for tenant %d", tenantID)
		}
	}

	// Remove inactive tenants
	for tenantID := range tm.activeTenants {
		found := false
		for _, id := range tenantIDs {
			if id == tenantID {
				found = true
				break
			}
		}
		if !found {
			if t, exists := tm.tenantManagers[tenantID]; exists {
				t.stop()
				delete(tm.tenantManagers, tenantID)
				delete(tm.activeTenants, tenantID)
				tm.log.Printf("removed tenant manager instance for tenant %d", tenantID)
			}
		}
	}
}

// createTenantInstance creates a new tenant manager instance.
func (tm *TenantManager) createTenantInstance(tenantID int) error {
	// Load tenant-specific configuration
	tenantCfg, err := tm.loadTenantConfig(tenantID)
	if err != nil {
		return fmt.Errorf("failed to load tenant config: %v", err)
	}

	// Create tenant instance
	instance := &tenantInstanceManager{
		tenantID:     tenantID,
		cfg:          tenantCfg,
		store:        tm.tenantStore,
		i18n:         tm.i18n,
		fnNotify:     tm.fnNotify,
		log:          tm.log,
		pipes:        make(map[int]*tenantPipe),
		tpls:         make(map[int]*models.Template),
		links:        make(map[string]string),
		nextPipes:    make(chan *tenantPipe, 1000),
		campMsgQ:     make(chan TenantCampaignMessage, tenantCfg.Concurrency*tenantCfg.MessageRate*2),
		msgQ:         make(chan models.Message, tenantCfg.Concurrency*tenantCfg.MessageRate*2),
		slidingStart: time.Now(),
		active:       true,
		stopCh:       make(chan struct{}),
		messengers:   make(map[string]Messenger),
		tplFuncs:     tm.tplFuncs,
	}

	// Start tenant instance
	instance.wg.Add(1)
	go instance.run()

	tm.tenantManagers[tenantID] = instance
	return nil
}

// getActiveTenantIDs retrieves list of active tenant IDs.
// This would need to be implemented based on your tenant discovery logic.
func (tm *TenantManager) getActiveTenantIDs() ([]int, error) {
	// This is a placeholder - you would implement this to query the database
	// for active tenants that have campaigns to process
	return []int{1}, nil // Return default tenant for now
}

// loadTenantConfig loads tenant-specific configuration.
func (tm *TenantManager) loadTenantConfig(tenantID int) (TenantConfig, error) {
	// Load tenant settings from database
	settings, err := tm.tenantStore.GetTenantSettings(tenantID)
	if err != nil {
		return TenantConfig{}, fmt.Errorf("failed to get tenant settings: %v", err)
	}

	// Create tenant-specific config based on base config and tenant settings
	tenantCfg := TenantConfig{
		Config:   tm.cfg,
		TenantID: tenantID,
	}

	// Apply tenant-specific settings
	if fromEmail, ok := settings["from_email"].(string); ok {
		tenantCfg.TenantFromEmail = fromEmail
	} else {
		tenantCfg.TenantFromEmail = tm.cfg.FromEmail
	}

	if smtpHost, ok := settings["smtp_host"].(string); ok {
		tenantCfg.TenantSMTPHost = smtpHost
	}

	if smtpPort, ok := settings["smtp_port"].(float64); ok {
		tenantCfg.TenantSMTPPort = int(smtpPort)
	}

	if smtpUsername, ok := settings["smtp_username"].(string); ok {
		tenantCfg.TenantSMTPUsername = smtpUsername
	}

	if smtpPassword, ok := settings["smtp_password"].(string); ok {
		tenantCfg.TenantSMTPPassword = smtpPassword
	}

	// URLs with tenant context
	tenantCfg.TenantRootURL = fmt.Sprintf("%s/tenant/%d", tm.cfg.RootURL, tenantID)
	tenantCfg.TenantUnsubURL = fmt.Sprintf("%s/tenant/%d/subscription/%%s/%%s", tm.cfg.RootURL, tenantID)
	tenantCfg.TenantOptinURL = fmt.Sprintf("%s/tenant/%d/subscription/optin/%%s?l=%%s", tm.cfg.RootURL, tenantID)
	tenantCfg.TenantMessageURL = fmt.Sprintf("%s/tenant/%d/campaign/%%s/%%s", tm.cfg.RootURL, tenantID)
	tenantCfg.TenantArchiveURL = fmt.Sprintf("%s/tenant/%d/archive", tm.cfg.RootURL, tenantID)

	// Apply tenant-specific limits if present
	if batchSize, ok := settings["max_batch_size"].(float64); ok && batchSize > 0 {
		tenantCfg.TenantMaxBatchSize = int(batchSize)
	} else {
		tenantCfg.TenantMaxBatchSize = tm.cfg.BatchSize
	}

	if concurrency, ok := settings["max_concurrency"].(float64); ok && concurrency > 0 {
		tenantCfg.TenantMaxConcurrency = int(concurrency)
	} else {
		tenantCfg.TenantMaxConcurrency = tm.cfg.Concurrency
	}

	if msgRate, ok := settings["message_rate"].(float64); ok && msgRate > 0 {
		tenantCfg.TenantMessageRate = int(msgRate)
	} else {
		tenantCfg.TenantMessageRate = tm.cfg.MessageRate
	}

	if maxErrors, ok := settings["max_send_errors"].(float64); ok && maxErrors > 0 {
		tenantCfg.TenantMaxSendErrors = int(maxErrors)
	} else {
		tenantCfg.TenantMaxSendErrors = tm.cfg.MaxSendErrors
	}

	return tenantCfg, nil
}

// scanActiveTenants periodically scans all active tenants for campaigns to process.
func (tm *TenantManager) scanActiveTenants(interval time.Duration) {
	defer tm.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.tenantManagersMut.RLock()
			for _, instance := range tm.tenantManagers {
				if instance.IsActive() {
					instance.triggerCampaignScan()
				}
			}
			tm.tenantManagersMut.RUnlock()
		case <-tm.shutdownCh:
			return
		}
	}
}

// makeGenericFuncMap returns generic template functions for tenant manager.
func (tm *TenantManager) makeGenericFuncMap() template.FuncMap {
	funcs := template.FuncMap{
		"Date": func(layout string) string {
			if layout == "" {
				layout = time.ANSIC
			}
			return time.Now().Format(layout)
		},
		"L": func() *i18n.I18n {
			return tm.i18n
		},
		"Safe": func(safeHTML string) template.HTML {
			return template.HTML(safeHTML)
		},
	}

	// Copy sprig functions.
	sprigFuncs := sprig.GenericFuncMap()
	delete(sprigFuncs, "env")
	delete(sprigFuncs, "expandenv")

	maps.Copy(funcs, sprigFuncs)

	return funcs
}

// scanCampaigns is a blocking function that periodically scans the data source
// for campaigns to process and dispatches them to the manager. It feeds campaigns
// into nextPipes.
func (m *Manager) scanCampaigns(tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()

	// Periodically scan the data source for campaigns to process.
	for range t.C {
		ids, counts := m.getCurrentCampaigns()
		campaigns, err := m.store.NextCampaigns(ids, counts)
		if err != nil {
			m.log.Printf("error fetching campaigns: %v", err)
			continue
		}

		for _, c := range campaigns {
			// Create a new pipe that'll handle this campaign's states.
			p, err := m.newPipe(c)
			if err != nil {
				m.log.Printf("error processing campaign (%s): %v", c.Name, err)
				continue
			}
			m.log.Printf("start processing campaign (%s)", c.Name)

			// If subscriber processing is busy, move on. Blocking and waiting
			// can end up in a race condition where the waiting campaign's
			// state in the data source has changed.
			select {
			case m.nextPipes <- p:
			default:
			}
		}
	}
}

// worker is a blocking function that perpetually listents to events (message) on different
// queues and processes them.
func (m *Manager) worker() {
	// Counter to keep track of the message / sec rate limit.
	numMsg := 0
	for {
		select {
		// Campaign message.
		case msg, ok := <-m.campMsgQ:
			if !ok {
				return
			}

			// If the campaign has ended or stopped, ignore the message.
			if msg.pipe != nil && msg.pipe.stopped.Load() {
				// Reduce the message counter on the pipe.
				msg.pipe.wg.Done()
				continue
			}

			// Pause on hitting the message rate.
			if numMsg >= m.cfg.MessageRate {
				time.Sleep(time.Second)
				numMsg = 0
			}
			numMsg++

			// Outgoing message.
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

			// Attach List-Unsubscribe headers?
			if m.cfg.UnsubHeader {
				h.Set("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
				h.Set("List-Unsubscribe", `<`+msg.unsubURL+`>`)
			}

			// Attach any custom headers.
			if len(msg.Campaign.Headers) > 0 {
				for _, set := range msg.Campaign.Headers {
					for hdr, val := range set {
						h.Add(hdr, val)
					}
				}
			}

			// Set the headers.
			out.Headers = h

			// Push the message to the messenger.
			err := m.messengers[msg.Campaign.Messenger].Push(out)
			if err != nil {
				m.log.Printf("error sending message in campaign %s: subscriber %d: %v", msg.Campaign.Name, msg.Subscriber.ID, err)
			}

			// Increment the send rate or the error counter if there was an error.
			if msg.pipe != nil {
				// Mark the message as done.
				msg.pipe.wg.Done()

				if err != nil {
					// Call the error callback, which keeps track of the error count
					// and stops the campaign if the error count exceeds the threshold.
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

		// Arbitrary message.
		case msg, ok := <-m.msgQ:
			if !ok {
				return
			}

			// Push the message to the messenger.
			if err := m.messengers[msg.Messenger].Push(msg); err != nil {
				m.log.Printf("error sending message '%s': %v", msg.Subject, err)
			}
		}
	}
}

// getCurrentCampaigns returns the IDs of campaigns currently being processed
// and their sent counts.
func (m *Manager) getCurrentCampaigns() ([]int64, []int64) {
	// Needs to return an empty slice in case there are no campaigns.
	m.pipesMut.RLock()
	defer m.pipesMut.RUnlock()

	var (
		ids    = make([]int64, 0, len(m.pipes))
		counts = make([]int64, 0, len(m.pipes))
	)
	for _, p := range m.pipes {
		ids = append(ids, int64(p.camp.ID))

		// Get the sent counts for campaigns and reset them to 0
		// as in the database, they're stored cumulatively (sent += $newSent).
		counts = append(counts, p.sent.Load())
		p.sent.Store(0)
	}

	return ids, counts
}

// trackLink register a URL and return its UUID to be used in message templates
// for tracking links.
func (m *Manager) trackLink(url, campUUID, subUUID string) string {
	url = strings.ReplaceAll(url, "&amp;", "&")

	m.linksMut.RLock()
	if uu, ok := m.links[url]; ok {
		m.linksMut.RUnlock()
		return fmt.Sprintf(m.cfg.LinkTrackURL, uu, campUUID, subUUID)
	}
	m.linksMut.RUnlock()

	// Register link.
	uu, err := m.store.CreateLink(url)
	if err != nil {
		m.log.Printf("error registering tracking for link '%s': %v", url, err)

		// If the registration fails, fail over to the original URL.
		return url
	}

	m.linksMut.Lock()
	m.links[url] = uu
	m.linksMut.Unlock()

	return fmt.Sprintf(m.cfg.LinkTrackURL, uu, campUUID, subUUID)
}

// sendNotif sends a notification to registered admin e-mails.
func (m *Manager) sendNotif(c *models.Campaign, status, reason string) error {
	var (
		subject = fmt.Sprintf("%s: %s", cases.Title(language.Und).String(status), c.Name)
		data    = map[string]any{
			"ID":     c.ID,
			"Name":   c.Name,
			"Status": status,
			"Sent":   c.Sent,
			"ToSend": c.ToSend,
			"Reason": reason,
		}
	)

	return m.fnNotify(subject, data)
}

// makeGnericFuncMap returns a generic template func map with custom template
// functions and sprig template functions.
func (m *Manager) makeGnericFuncMap() template.FuncMap {
	funcs := template.FuncMap{
		"Date": func(layout string) string {
			if layout == "" {
				layout = time.ANSIC
			}
			return time.Now().Format(layout)
		},
		"L": func() *i18n.I18n {
			return m.i18n
		},
		"Safe": func(safeHTML string) template.HTML {
			return template.HTML(safeHTML)
		},
	}

	// Copy spring functions.
	sprigFuncs := sprig.GenericFuncMap()
	delete(sprigFuncs, "env")
	delete(sprigFuncs, "expandenv")

	maps.Copy(funcs, sprigFuncs)

	return funcs
}

// attachMedia loads any media/attachments from the media store and attaches
// the byte blobs to the campaign.
func (m *Manager) attachMedia(c *models.Campaign) error {
	// Load any media/attachments.
	for _, mid := range []int64(c.MediaIDs) {
		a, err := m.store.GetAttachment(int(mid))
		if err != nil {
			return fmt.Errorf("error fetching attachment %d on campaign %s: %v", mid, c.Name, err)
		}

		c.Attachments = append(c.Attachments, a)
	}

	return nil
}

// MakeAttachmentHeader is a helper function that returns a
// textproto.MIMEHeader tailored for attachments, primarily
// email. If no encoding is given, base64 is assumed.
func MakeAttachmentHeader(filename, encoding, contentType string) textproto.MIMEHeader {
	if encoding == "" {
		encoding = "base64"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", "attachment; filename="+filename)
	h.Set("Content-Type", fmt.Sprintf("%s; name=\""+filename+"\"", contentType))
	h.Set("Content-Transfer-Encoding", encoding)
	return h
}
