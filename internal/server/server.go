package server

import (
	"context"
	"log"
	"net/http"

	"os"

	"path/filepath"

	"strings"
	"sync"
	"time"

	"x-science/internal/account"
	adaptercommon "x-science/internal/adapters/common"
	adapterfeishu "x-science/internal/adapters/feishu"
	adapterwechat "x-science/internal/adapters/wechat"
	"x-science/internal/agentruntime"

	"x-science/internal/capabilities"
	"x-science/internal/cloudstore"
	compute "x-science/internal/compute"
	"x-science/internal/datadir"
	kernelruntime "x-science/internal/kernel"
	"x-science/internal/literaturediscovery"
	"x-science/internal/mcpdirectory"
	"x-science/internal/memoryconfig"
	"x-science/internal/observability"
	eventjournal "x-science/internal/persistence/journal"
	pairingstore "x-science/internal/persistence/pairing"
	runtimekv "x-science/internal/persistence/runtimekv"
	secretstore "x-science/internal/persistence/secrets"
	sessionstore "x-science/internal/persistence/sessions"
	settingsstore "x-science/internal/persistence/settings"
	taskruns "x-science/internal/persistence/taskruns"
	taskstore "x-science/internal/persistence/tasks"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	pluginhost "x-science/internal/plugins/host"

	runtimecontrol "x-science/internal/runtimecontrol"
	"x-science/internal/sciencecapability"
	"x-science/internal/skills"
	"x-science/internal/xsciencelink"

	"x-science/internal/tools/mcpstdio"

	"x-science/internal/tools/registry"
	"x-science/internal/tools/shellops"
	"x-science/internal/tools/webfetch"
	"x-science/internal/tools/websearch"
	"x-science/internal/vmresources"
	"x-science/internal/vmrestart"
)

type Options struct {
	Capabilities                          capabilities.Report
	XScienceLink                          *xsciencelink.Service
	XScienceLinkAuth                      XScienceLinkAuthOptions
	WebAuth                               WebAuthOptions
	LinkPackagePath                       string
	Tools                                 *registry.Registry
	Operations                            *registry.Registry
	Plugins                               *pluginhost.Host
	FileRoot                              string
	RuntimeAssetsDir                      string
	ComputeRemoteDialer                   compute.RemoteDialer
	ManagedEndpointRuntime                compute.ManagedEndpointOwnerRuntime
	ComputeProviderDial                   kernelruntime.ProviderProxyDialFunc
	ProviderOperationRunner               kernelruntime.ProviderOperationRunner
	HostGPUDetector                       func(context.Context) compute.GPUInfo
	ModalConfigPath                       string
	SkillDirectories                      []string
	SkillCatalog                          *skills.Catalog
	ScienceCapabilities                   *sciencecapability.Catalog
	AgentCatalog                          *agentruntime.AgentCatalog
	AgentCatalogRoot                      string
	AgentManifestPath                     string
	FeishuDeviceQR                        *adapterfeishu.DeviceQRLoginManager
	WeChatQR                              *adapterwechat.QRLoginManager
	HTTPClient                            *http.Client
	WebFetchOptions                       webfetch.Options
	WebSearchOptions                      websearch.Options
	PublicScientificResponseHeaderTimeout time.Duration
	PublicScientificTransferIdleTimeout   time.Duration
	VerifierToken                         string
	Feedback                              FeedbackOptions
	CompactSummarizer                     SessionRunnerChatOptions
	RunnerDiagnostics                     RunnerDiagnostics
	AdapterDiagnostics                    AdapterDiagnostics
	IMOutbound                            adaptercommon.SessionOutboundSink
	Workspace                             *workspace.Store
	RuntimeStore                          *runtimekv.Store
	Transcript                            *transcriptstore.Repository
	TranscriptWebReadModel                *transcriptstore.WebReadModelRepository
	MemoryConfig                          *memoryconfig.Config
	MCPDirectory                          *mcpdirectory.Service
	RCSBFiles                             RCSBFileFetcher
	RCSBSearch                            RCSBStructureSearcher
	PublicScientificFiles                 PublicScientificFileFetcher
	KernelManager                         *kernelruntime.Manager
	KernelExecutionBackend                kernelruntime.ExecutionBackend
	DataDirSource                         string
	DataDirControlPath                    string
	DefaultDataDir                        string
	CondaHome                             string
	CondaEnvsPath                         string
	ConfigAllowedDomains                  []string
	ConfigDeniedDomains                   []string
	ConfigNetworkProxy                    string
	ConfigNetworkProxyInherited           bool
	MCPX509Posture                        func() mcpstdio.TLSPosture
	RestartRuntime                        func(reason string) error
	RuntimeUpdate                         RuntimeUpdateOptions
	StartBackgroundServices               bool
	HostDirectoryPicker                   func(context.Context) (string, error)
	VMRestart                             *vmrestart.Manager
	VMResources                           *vmresources.Controller
	CloudFactory                          cloudstore.ClientFactory
	WebUI                                 http.Handler
	// TryDrainIdleDurableWait is invoked after idle admission is fenced and
	// before durable liveness reads. Production callers leave it nil.
	TryDrainIdleDurableWait func()
}

type RunnerDiagnostics struct {
	Enabled               bool
	Provider              string
	RunnerID              string
	CommandConfigured     bool
	ChatEndpoint          string
	ChatModel             string
	ChatAPIKeySet         bool
	ChatAPIKeySource      string
	ChatTools             []string
	ChatToolRoundLimit    int
	PollIntervalMS        int
	LeaseTTLSeconds       int
	ReplayLimit           int64
	OutputLimitBytes      int64
	RuntimeModelAuthority bool
}

type AdapterDiagnostics struct {
	Platforms []AdapterPlatformDiagnostics
}

type AdapterPlatformDiagnostics struct {
	Name               string
	Enabled            bool
	Configured         bool
	InboundConfigured  bool
	OutboundConfigured bool
	Endpoint           string
	CredentialFields   map[string]bool
	CredentialValues   map[string]string `json:"-"`
}

type Server struct {
	capabilities     capabilities.Report
	xscienceLink     *xsciencelink.Service
	xscienceLinkAuth *xscienceLinkAuthenticator
	loginLimiter     *loginRateLimiter
	webAuthenticationState
	accountManagementReader               account.ManagementReader
	linkPackagePath                       string
	tools                                 *registry.Registry
	operations                            *registry.Registry
	plugins                               *pluginhost.Host
	fileRoot                              string
	workspaceDirectorySyncer              workspaceDirectorySyncer
	managedEndpointRuntime                compute.ManagedEndpointOwnerRuntime
	runtimeAssetsDir                      string
	settingsStore                         *settingsstore.Store
	secretStore                           *secretstore.Store
	sessionStore                          *sessionstore.Store
	computeRemoteDialer                   compute.RemoteDialer
	computeProviderDial                   kernelruntime.ProviderProxyDialFunc
	computeProviderProvisionWake          chan string
	computeProviderJobWake                chan struct{}
	providerOperationRunner               kernelruntime.ProviderOperationRunner
	computeProviderJobsMu                 sync.Mutex
	computeProviderJobs                   map[string]bool
	computeProviderHandleMu               sync.Mutex
	computeSubmitMu                       sync.Mutex
	hostGPUDetector                       func(context.Context) compute.GPUInfo
	modalConfigPath                       string
	eventJournal                          *eventjournal.EventJournal
	taskStore                             *taskstore.Store
	taskRunStore                          *taskruns.Store
	pairingStore                          *pairingstore.Store
	runtimeStore                          *runtimekv.Store
	runtimeStoreOwned                     bool
	usageScanner                          *runtimecontrol.Scanner
	kernelManager                         *kernelruntime.Manager
	kernelExecutionBackend                kernelruntime.ExecutionBackend
	kernelDiscoveryErr                    error
	kernelConfinement                     func(bool) kernelruntime.ConfinementEvidence
	dataDirectoryController               *datadir.Controller
	dataDirectorySource                   string
	defaultDataDirectory                  string
	condaHome                             string
	condaEnvsPath                         string
	configAllowedDomains                  []string
	configDeniedDomains                   []string
	configNetworkProxy                    string
	publicSourceProxy                     string
	mcpX509Posture                        func() mcpstdio.TLSPosture
	allowedDomainsMu                      sync.Mutex
	hostGrantKernelMu                     sync.Mutex
	hostGrantKernelFences                 map[string]bool
	hostGrantKernelFenceEpoch             map[string]uint64
	kernelLocalExecMu                     sync.Mutex
	kernelLocalExecWaiters                map[string]kernelLocalExecWaiterAuthority
	detachedKernelObserverMu              sync.Mutex
	detachedKernelObservers               map[string]context.CancelFunc
	detachedKernelObserversDone           chan struct{}
	detachedKernelObserversDraining       bool
	kernelOperationBootID                 string
	approvalDecisionMu                    *sync.Mutex
	restartRuntime                        func(reason string) error
	runtimeUpdate                         *runtimeUpdateController
	hostDirectoryPicker                   func(context.Context) (string, error)
	vmRestart                             *vmrestart.Manager
	vmResources                           *vmresources.Controller
	cloudFactory                          cloudstore.ClientFactory
	workspaceStore                        *workspace.Store
	transcriptStore                       *transcriptstore.Repository
	transcriptWebReadModel                *transcriptstore.WebReadModelRepository
	memoryConfig                          memoryconfig.Config
	memoryExtraction                      *memoryExtractionRuntime
	memoryAutoExtractionCommitMu          sync.RWMutex
	transcriptContractErr                 error
	transcriptDeliveryMu                  sync.Mutex
	transcriptDeliveryStop                context.CancelFunc
	transcriptDeliveryDone                chan struct{}
	transcriptDeliveryWake                chan struct{}
	dataLifecycleStop                     context.CancelFunc
	dataLifecycleDone                     chan struct{}
	frameResumeDispatchWakeMu             sync.Mutex
	frameResumeDispatchWake               chan struct{}
	mcpDirectory                          *mcpdirectory.Service
	mcpDirectoryOwned                     bool
	mcpApps                               *mcpAppBroker
	backgroundServicesStarted             bool
	scientificRuntimeWarmupMu             sync.RWMutex
	scientificRuntimeWarmups              map[string]scientificRuntimeWarmupStatus
	scientificRuntimeWarmupCancels        map[string]context.CancelFunc
	scientificRuntimeWarmupCancelled      map[string]bool
	scientificRuntimeWarmupWake           chan string
	runtimeStartedAt                      time.Time
	mcpDiscoverySlots                     chan struct{}
	rcsbFiles                             RCSBFileFetcher
	rcsbSearch                            RCSBStructureSearcher
	publicScientificFiles                 PublicScientificFileFetcher
	publicScientificResponseHeaderTimeout time.Duration
	publicScientificTransferIdleTimeout   time.Duration
	publicScientificDownloadSlots         chan struct{}
	publicScientificDownloadCacheMu       sync.Mutex
	publicScientificDownloadCacheFlights  map[string]*agentPublicScientificDownloadFlight
	mcpAppResourceTickets                 *mcpAppResourceTicketStore
	workspaceEvents                       *workspaceEventHub
	compatEvents                          *compatEventHub
	httpClient                            *http.Client
	literatureRelatedOnce                 sync.Once
	literatureRelated                     *literaturediscovery.Finder
	publicationChecker                    *literaturediscovery.PublicationChecker
	speechClientForURL                    func(context.Context, string, *http.Client) (*http.Client, error)
	localSpeechRuntime                    *localSpeechRuntime
	webImageClientForURL                  func(context.Context, string, *http.Client) (*http.Client, error)
	verifierToken                         string
	feedback                              FeedbackOptions
	feishuDeviceQR                        *adapterfeishu.DeviceQRLoginManager
	wechatQR                              *adapterwechat.QRLoginManager
	messageChannelQRMu                    sync.Mutex
	messageChannelQRBindings              map[string]messageChannelQRBinding
	sessionSockets                        *sessionWebSocketHub
	sessionRunsMu                         sync.Mutex
	sessionRuns                           map[string]*activeSessionRun
	delegatedStopProofsMu                 sync.Mutex
	delegatedStopProofs                   map[delegatedStopCompletionKey]*delegatedStopPendingProof
	activeRuntimeOperations               int
	sessionRunsDraining                   bool
	sessionRunsDrainEvaluating            bool
	tryDrainIdleDurableWait               func()
	manualReviewMu                        sync.Mutex
	visualReviewChallengeToken            func() (string, error)
	runtimeComponentsMu                   sync.RWMutex
	runtimeComponents                     map[string]runtimeComponentState
	kernelPeerPendingMu                   sync.Mutex
	kernelPeerPending                     map[string]int
	kernelPeerReservations                map[string]string
	agentKernelForegroundWaitTimeout      time.Duration
	kernelIdleNow                         func() time.Time
	feishuDedup                           *adaptercommon.MessageDedup
	wechatDedup                           *adaptercommon.MessageDedup
	skillCatalog                          *skills.Catalog
	scienceCapabilities                   *sciencecapability.Catalog
	skillErrors                           []skills.LoadError
	skillDirectories                      []string
	skillMutationMu                       sync.Mutex
	agentCatalog                          *agentruntime.AgentCatalog
	agentCatalogError                     error
	csrfToken                             string
	compactSummarizer                     SessionRunnerChatOptions
	runnerDiagnostics                     RunnerDiagnostics
	adapterDiagnostics                    AdapterDiagnostics
	imOutbound                            adaptercommon.SessionOutboundSink
	transcriptIMMu                        sync.Mutex
	transcriptIMClaims                    map[transcriptIMClaimKey]transcriptstore.DeliveryClaim
	transcriptIMRoutes                    map[string]string
	imInboundMu                           sync.Mutex
	imInboundLocks                        map[string]*imInboundSessionLock
	imProjectionHook                      func()
	compatRequestMu                       sync.Mutex
	agentToolApprovalMu                   sync.Mutex
	sessionSubmissionMu                   sync.Mutex
	readStateMu                           sync.Mutex
	readState                             map[string]fileReadSnapshot
	readCursorCache                       canonicalReadCursorCache
	transcriptWebCache                    transcriptWebProjectionCache
	streamingCacheMu                      sync.Mutex
	streamingCache                        map[string]*frameStreamingCacheEntry
	projectionRetryMu                     sync.Mutex
	projectionRetryAt                     map[string]time.Time
	transcriptWebCatchupMu                sync.Mutex
	transcriptWebCatchups                 map[transcriptWebCatchupKey]*transcriptWebCatchupFlight
	streamingCacheClock                   uint64
	backgroundShellMu                     sync.Mutex
	backgroundShells                      map[string]*shellops.RunningCommand
	backgroundShellWG                     sync.WaitGroup
	backgroundShellClosing                bool
	backgroundShellErr                    error
	backgroundShellDone                   chan struct{}
	webResearchMu                         sync.Mutex
	webResearchSessions                   map[string]*webResearchSessionState
	webZipMu                              sync.Mutex
	webZipCancels                         map[string]context.CancelFunc
	webSnapshotMu                         sync.Mutex
	webSnapshots                          map[string]*webFSSnapshot
	webPreviewHistoryMu                   sync.Mutex
	webOfficeMu                           sync.Mutex
	provenanceCensusCache                 provenanceCensusCache
	webOfficeWatches                      map[string]*webOfficeWatch
	webShellLauncher                      webShellLauncher
	webUI                                 http.Handler
	httpMetrics                           *observability.HTTPRegistry
}

func New(options Options) *Server {
	runtimeMemoryConfig := memoryconfig.Default()
	if options.MemoryConfig != nil {
		runtimeMemoryConfig = *options.MemoryConfig
	}
	report := options.Capabilities
	if report.Status == "" {
		report = capabilities.CompactXScience()
	}
	link := options.XScienceLink
	if link == nil {
		link = xsciencelink.NewService()
	}
	remoteDialer := options.ComputeRemoteDialer
	if remoteDialer == nil {
		remoteDialer = compute.OpenSSHRemoteDialer{}
	}
	providerOperationRunner := options.ProviderOperationRunner
	hostGPUDetector := options.HostGPUDetector
	if hostGPUDetector == nil {
		hostGPUDetector = compute.DetectHostGPU
	}
	tools := options.Tools
	operations := options.Operations
	registeredWebSearchOptions, publicSourceProxy := configurePublicSourceOptions(&options)
	if tools != nil && operations == nil {
		catalogs := registry.Split(tools)
		tools = catalogs.ModelTools
		operations = catalogs.Operations
	} else if tools == nil || operations == nil {
		catalogs := registry.DefaultCatalogs()
		if tools == nil {
			tools = catalogs.ModelTools
			tools.SetWebOptions(options.WebFetchOptions, registeredWebSearchOptions)
		}
		if operations == nil {
			operations = catalogs.Operations
			operations.SetWebOptions(options.WebFetchOptions, registeredWebSearchOptions)
		}
	}
	tools.AttachServiceOperations(operations)
	plugins := options.Plugins
	if plugins == nil {
		plugins = pluginhost.NewDefault()
	}
	wechatQR := options.WeChatQR
	if wechatQR == nil {
		wechatQR = adapterwechat.NewQRLoginManager("")
	}
	feishuDeviceQR := options.FeishuDeviceQR
	if feishuDeviceQR == nil {
		feishuDeviceQR = adapterfeishu.NewDeviceQRLoginManager("")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cloudFactory := options.CloudFactory
	if cloudFactory == nil {
		cloudFactory = cloudstore.NewFactory(httpClient)
	}
	mcpDirectory := options.MCPDirectory
	mcpDirectoryOwned := false
	if mcpDirectory == nil && options.Workspace != nil && strings.TrimSpace(options.FileRoot) != "" {
		mcpDirectory = mcpdirectory.New(options.Workspace, options.FileRoot, httpClient, publicSourceProxy)
		mcpDirectoryOwned = true
	}
	if mcpDirectory != nil && options.Workspace != nil {
		mcpDirectory.SetContactEmailProvider(func(userID string) (string, bool, error) {
			decision, found, err := options.Workspace.LatestContactEmailDecision(strings.TrimSpace(userID))
			if err != nil || !found || decision.Decision != workspace.ContactEmailDecisionAllowed ||
				decision.NoticeVersion != contactEmailNoticeVersion() || strings.TrimSpace(decision.Email) == "" {
				return "", false, err
			}
			return strings.TrimSpace(decision.Email), true, nil
		})
	}
	if mcpDirectory != nil {
		mcpDirectory.SetTLSPostureProvider(options.MCPX509Posture)
	}
	skillCatalog, skillDirectories, skillErrors := loadSkillCatalog(options.SkillCatalog, options.SkillDirectories, options.FileRoot)
	agentCatalog, agentCatalogErr := loadBundledAgentCatalog(options, skillCatalog)
	scienceCapabilities := options.ScienceCapabilities
	if scienceCapabilities == nil {
		if embedded, err := sciencecapability.DefaultCatalog(); err == nil {
			scienceCapabilities = &embedded
		}
	}
	var settings *settingsstore.Store
	var secrets *secretstore.Store
	var sessions *sessionstore.Store
	var journal *eventjournal.EventJournal
	var tasks *taskstore.Store
	var taskRuns *taskruns.Store
	var pairing *pairingstore.Store
	runtime := options.RuntimeStore
	runtimeOwned := false
	var usage *runtimecontrol.Scanner
	if options.FileRoot != "" {
		settings = settingsstore.New(filepath.Join(options.FileRoot, "settings.json"))
		secrets = secretstore.New(options.FileRoot)
		sessions = sessionstore.NewStore(options.FileRoot)
		journal = eventjournal.NewEventJournal(options.FileRoot)
		tasks = taskstore.New(filepath.Join(options.FileRoot, "tasks.json"))
		taskRuns = taskruns.NewStore(options.FileRoot)
		pairing = pairingstore.New(filepath.Join(options.FileRoot, "pairing.json"))
		if runtime == nil {
			runtime = runtimekv.New(filepath.Join(options.FileRoot, "runtime-state.sqlite"))
			runtimeOwned = true
		}
		condaHome := strings.TrimSpace(options.CondaHome)
		if condaHome == "" {
			condaHome = runtimecontrol.CondaRoot(options.FileRoot)
		}
		condaEnvsPath := strings.TrimSpace(options.CondaEnvsPath)
		if condaEnvsPath == "" {
			condaEnvsPath = filepath.Join(condaHome, "envs")
		}
		usage = runtimecontrol.NewScannerWithCondaEnvs(
			options.FileRoot, condaHome, condaEnvsPath, 5*time.Minute,
		)
	}
	if mcpDirectory != nil && secrets != nil {
		mcpDirectory.SetBundledAPIKeyProvider(func(userID, connectorID string) (string, bool, error) {
			secret, found, err := secrets.ResolveForUser(bundledMCPAPIKeySecretID(userID, connectorID), userID)
			if err != nil || !found {
				return "", found, err
			}
			value := strings.TrimSpace(secret.Value)
			return value, value != "", nil
		})
	}
	approvalDecisionMu := &sync.Mutex{}
	if settings != nil {
		link.SetPolicyDefaultsProvider(func() xsciencelink.PolicyDefaults {
			setting, ok, err := settings.Get(approvalDefaultsSettingKey)
			if err != nil || !ok {
				return xsciencelink.PolicyDefaults{}
			}
			defaults, err := xsciencelink.NormalizePolicyDefaults(setting.Value)
			if err != nil {
				return xsciencelink.PolicyDefaults{}
			}
			return defaults
		})
		link.SetRememberedApprovalStore(
			func(userID, clientID, action string) (xsciencelink.RememberedApprovalDecision, bool) {
				setting, ok, err := settings.Get(approvalRememberedSettingKey)
				if err != nil || !ok {
					return xsciencelink.RememberedApprovalDecision{}, false
				}
				decisions := rememberedApprovalDecisionsFromSetting(setting.Value)
				decision, ok := decisions[xsciencelink.RememberedApprovalKey(userID, clientID, action)]
				return decision, ok
			},
			func(decision xsciencelink.RememberedApprovalDecision) error {
				approvalDecisionMu.Lock()
				defer approvalDecisionMu.Unlock()
				setting, ok, err := settings.Get(approvalRememberedSettingKey)
				if err != nil {
					return err
				}
				decisions := map[string]xsciencelink.RememberedApprovalDecision{}
				if ok {
					decisions = rememberedApprovalDecisionsFromSetting(setting.Value)
				}
				decisions[xsciencelink.RememberedApprovalKey(decision.UserID, decision.ClientID, decision.Action)] = decision
				_, err = settings.Set(approvalRememberedSettingKey, rememberedApprovalDecisionsToSetting(decisions))
				return err
			},
		)
	}
	kernelManager := options.KernelManager
	var kernelDiscoveryErr error
	if kernelManager == nil && options.StartBackgroundServices {
		kernelManager, kernelDiscoveryErr = kernelruntime.DiscoverManagerWithPathsAndProxy(options.CondaHome, options.CondaEnvsPath, options.ConfigNetworkProxy)
	}
	if providerOperationRunner == nil && kernelManager != nil {
		providerOperationRunner = kernelManager
	}
	if mcpDirectory != nil && kernelManager != nil {
		mcpDirectory.SetBundledPythonCommandProvider(kernelManager.BundledMCPPythonExecutable)
	}
	var kernelConfinement func(bool) kernelruntime.ConfinementEvidence
	if kernelManager != nil {
		kernelConfinement = func(retry bool) kernelruntime.ConfinementEvidence {
			if retry {
				return kernelManager.RetryConfinementEvidence()
			}
			return kernelManager.ConfinementEvidence()
		}
	}
	var dataDirectoryController *datadir.Controller
	if strings.TrimSpace(options.DataDirControlPath) != "" {
		dataDirectoryController = datadir.New(options.DataDirControlPath)
	}
	modalConfigPath := strings.TrimSpace(options.ModalConfigPath)
	if modalConfigPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			modalConfigPath = filepath.Join(home, ".modal.toml")
		}
	}
	linkAuth := newXScienceLinkAuthenticator(options.XScienceLinkAuth)
	webAuthState := newWebAuthenticationState(options, linkAuth, httpClient)
	var transcriptContractErr error
	if options.Transcript != nil {
		transcriptContractErr = options.Transcript.ValidateCurrentContract(context.Background())
		if transcriptContractErr == nil {
			_, transcriptContractErr = options.Transcript.ReconcileActiveHistoryRealtimeRebases(context.Background(), 1000)
		}
	}
	server := &Server{
		capabilities:           report,
		xscienceLink:           link,
		xscienceLinkAuth:       linkAuth,
		loginLimiter:           newLoginRateLimiter(),
		webAuthenticationState: webAuthState,
		accountManagementReader: newLocalAccountManagementReaderWithConfig(webAuthState, linkAuth, localAccountManagementConfig{
			settings: settings, allowedDomains: options.ConfigAllowedDomains,
			deniedDomains: options.ConfigDeniedDomains, mcpConfigured: mcpDirectory != nil,
		}),
		linkPackagePath:                       options.LinkPackagePath,
		tools:                                 tools,
		operations:                            operations,
		plugins:                               plugins,
		fileRoot:                              options.FileRoot,
		managedEndpointRuntime:                options.ManagedEndpointRuntime,
		runtimeAssetsDir:                      cleanExistingDirectory(options.RuntimeAssetsDir),
		settingsStore:                         settings,
		secretStore:                           secrets,
		sessionStore:                          sessions,
		eventJournal:                          journal,
		taskStore:                             tasks,
		taskRunStore:                          taskRuns,
		pairingStore:                          pairing,
		runtimeStore:                          runtime,
		runtimeStoreOwned:                     runtimeOwned,
		usageScanner:                          usage,
		computeRemoteDialer:                   remoteDialer,
		computeProviderDial:                   options.ComputeProviderDial,
		computeProviderProvisionWake:          make(chan string, 8),
		computeProviderJobWake:                make(chan struct{}, 1),
		providerOperationRunner:               providerOperationRunner,
		computeProviderJobs:                   map[string]bool{},
		hostGPUDetector:                       hostGPUDetector,
		kernelManager:                         kernelManager,
		kernelExecutionBackend:                options.KernelExecutionBackend,
		kernelDiscoveryErr:                    kernelDiscoveryErr,
		kernelConfinement:                     kernelConfinement,
		modalConfigPath:                       modalConfigPath,
		dataDirectoryController:               dataDirectoryController,
		dataDirectorySource:                   options.DataDirSource,
		defaultDataDirectory:                  options.DefaultDataDir,
		condaHome:                             options.CondaHome,
		condaEnvsPath:                         options.CondaEnvsPath,
		configAllowedDomains:                  append([]string(nil), options.ConfigAllowedDomains...),
		configDeniedDomains:                   append([]string(nil), options.ConfigDeniedDomains...),
		configNetworkProxy:                    strings.TrimSpace(options.ConfigNetworkProxy),
		publicSourceProxy:                     publicSourceProxy,
		mcpX509Posture:                        options.MCPX509Posture,
		hostGrantKernelFences:                 map[string]bool{},
		hostGrantKernelFenceEpoch:             map[string]uint64{},
		detachedKernelObservers:               map[string]context.CancelFunc{},
		detachedKernelObserversDone:           nil,
		kernelOperationBootID:                 newCSRFToken(),
		approvalDecisionMu:                    approvalDecisionMu,
		restartRuntime:                        options.RestartRuntime,
		runtimeUpdate:                         newRuntimeUpdateController(options.RuntimeUpdate),
		hostDirectoryPicker:                   options.HostDirectoryPicker,
		vmRestart:                             options.VMRestart,
		vmResources:                           options.VMResources,
		cloudFactory:                          cloudFactory,
		workspaceStore:                        options.Workspace,
		transcriptStore:                       options.Transcript,
		transcriptWebReadModel:                options.TranscriptWebReadModel,
		memoryConfig:                          runtimeMemoryConfig,
		transcriptContractErr:                 transcriptContractErr,
		workspaceEvents:                       newWorkspaceEventHub(),
		compatEvents:                          newCompatEventHub(),
		mcpDirectory:                          mcpDirectory,
		mcpDirectoryOwned:                     mcpDirectoryOwned,
		mcpApps:                               newMCPAppBroker(),
		backgroundServicesStarted:             options.StartBackgroundServices,
		scientificRuntimeWarmups:              map[string]scientificRuntimeWarmupStatus{},
		scientificRuntimeWarmupCancels:        map[string]context.CancelFunc{},
		scientificRuntimeWarmupCancelled:      map[string]bool{},
		scientificRuntimeWarmupWake:           make(chan string, len(scientificRuntimeWarmupDefinitions())),
		runtimeStartedAt:                      time.Now().UTC(),
		mcpDiscoverySlots:                     processMCPDiscoverySlots,
		rcsbFiles:                             defaultRCSBFileFetcher(options.RCSBFiles, publicSourceProxy),
		rcsbSearch:                            defaultRCSBStructureSearcher(options.RCSBSearch, publicSourceProxy),
		publicScientificFiles:                 defaultPublicScientificFileFetcher(options.PublicScientificFiles, publicSourceProxy),
		publicScientificResponseHeaderTimeout: options.PublicScientificResponseHeaderTimeout,
		publicScientificTransferIdleTimeout:   options.PublicScientificTransferIdleTimeout,
		publicScientificDownloadSlots:         make(chan struct{}, agentPublicScientificDownloadConcurrency),
		httpClient:                            httpClient,
		localSpeechRuntime:                    newLocalSpeechRuntime(options.FileRoot, httpClient),
		verifierToken:                         strings.TrimSpace(options.VerifierToken),
		feedback:                              options.Feedback,
		feishuDeviceQR:                        feishuDeviceQR,
		wechatQR:                              wechatQR,
		messageChannelQRBindings:              map[string]messageChannelQRBinding{},
		sessionSockets:                        newSessionWebSocketHub(),
		sessionRuns:                           map[string]*activeSessionRun{},
		tryDrainIdleDurableWait:               options.TryDrainIdleDurableWait,
		agentKernelForegroundWaitTimeout:      defaultAgentKernelForegroundWaitTimeout,
		kernelIdleNow:                         time.Now,
		runtimeComponents:                     map[string]runtimeComponentState{},
		feishuDedup:                           adaptercommon.NewMessageDedup(30*time.Minute, 20000),
		wechatDedup:                           adaptercommon.NewMessageDedup(30*time.Minute, 20000),
		skillCatalog:                          skillCatalog,
		scienceCapabilities:                   scienceCapabilities,
		skillErrors:                           skillErrors,
		skillDirectories:                      skillDirectories,
		agentCatalog:                          agentCatalog,
		agentCatalogError:                     agentCatalogErr,
		csrfToken:                             newCSRFToken(),
		compactSummarizer:                     options.CompactSummarizer,
		runnerDiagnostics:                     options.RunnerDiagnostics,
		adapterDiagnostics:                    options.AdapterDiagnostics,
		imOutbound:                            normalizeIMOutboundSink(options.IMOutbound),
		transcriptIMClaims:                    map[transcriptIMClaimKey]transcriptstore.DeliveryClaim{},
		transcriptIMRoutes:                    map[string]string{},
		imInboundLocks:                        map[string]*imInboundSessionLock{},
		readState:                             map[string]fileReadSnapshot{},
		backgroundShells:                      map[string]*shellops.RunningCommand{},
		webResearchSessions:                   map[string]*webResearchSessionState{},
		webZipCancels:                         map[string]context.CancelFunc{},
		webSnapshots:                          map[string]*webFSSnapshot{},
		webOfficeWatches:                      map[string]*webOfficeWatch{},
		mcpAppResourceTickets:                 newMCPAppResourceTicketStore(),
		webUI:                                 options.WebUI,
		httpMetrics:                           observability.NewHTTPRegistry(),
	}
	server.publicationChecker = literaturediscovery.NewPublicationChecker(func(ctx context.Context, rawURL string) (*http.Client, error) {
		return server.publicSourceHTTPClient(ctx, rawURL, server.httpClient)
	}, server.workspaceStore)
	server.speechClientForURL = server.publicSourceHTTPClient
	server.webImageClientForURL = server.publicSourceHTTPClient
	server.bindIMRecoveryNoticeValidator()
	if mcpDirectory != nil {
		mcpDirectory.SetConnectorUsageRecorder(func(ctx context.Context, userID string, connector mcpdirectory.RuntimeConnector, toolName string, callErr error) {
			server.recordMCPConnectorInvocation(ctx, userID, connector.ID, connector.Source, connector.Name, toolName, callErr)
		})
	}
	if options.StartBackgroundServices {
		server.bindKernelStdoutRealtime(kernelManager)
	}
	if server.workspaceStore != nil {
		server.memoryExtraction = newMemoryExtractionRuntime(server)
	}
	if rules, err := server.loadStorageRules(); err == nil {
		server.configureStorageScanner(rules)
	} else if server.usageScanner != nil {
		log.Printf("storage rules scanner configuration failed: %T", err)
	}
	if err := server.compactToolGatewayAudits(); err != nil {
		log.Printf("tool gateway audit compaction failed: %T", err)
	}
	if options.StartBackgroundServices {
		server.recoverManagedEnvironmentObservations()
		server.startMemoryExtractionDelivery()
		server.startTranscriptWebDelivery()
		server.startDataLifecycle()
	}
	return server
}
