package start

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/alpacahq/marketstore/v4/executor"
	"github.com/alpacahq/marketstore/v4/frontend"
	"github.com/alpacahq/marketstore/v4/frontend/stream"
	"github.com/alpacahq/marketstore/v4/metrics"
	"github.com/alpacahq/marketstore/v4/plugins/trigger"
	pb "github.com/alpacahq/marketstore/v4/proto"
	"github.com/alpacahq/marketstore/v4/replication"
	"github.com/alpacahq/marketstore/v4/sqlparser"
	"github.com/alpacahq/marketstore/v4/utils"
	"github.com/alpacahq/marketstore/v4/utils/log"
)

const (
	usage                 = "start"
	short                 = "Start a marketstore database server"
	long                  = "This command starts a marketstore database server"
	example               = "marketstore start --config <path>"
	defaultConfigFilePath = "./mkts.yml"
	configDesc            = "set the path for the marketstore YAML configuration file"

	diskUsageMonitorInterval = 10 * time.Minute
)

var (
	// Cmd is the start command.
	Cmd = &cobra.Command{
		Use:        usage,
		Short:      short,
		Long:       long,
		Aliases:    []string{"s"},
		SuggestFor: []string{"boot", "up"},
		Example:    example,
		RunE:       executeStart,
	}
	// configFilePath set flag for a path to the config file.
	configFilePath string
)

// nolint:gochecknoinits // cobra's standard way to initialize flags
func init() {
	utils.InstanceConfig.StartTime = time.Now()
	Cmd.Flags().StringVarP(&configFilePath, "config", "c", defaultConfigFilePath, configDesc)
}

// executeStart implements the start command.
func executeStart(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	globalCtx, globalCancel := context.WithCancel(ctx)
	defer globalCancel()

	// Attempt to read config file.
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("failed to read configuration file error: %w", err)
	}

	// Don't output command usage if args(=only the filepath to mkts.yml at the moment) are correct
	cmd.SilenceUsage = true

	// Log config location.
	log.Info("using %v for configuration", configFilePath)

	// Attempt to set configuration.
	config, err := utils.InstanceConfig.Parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse configuration file error: %w", err)
	}

	// New gRPC stream server for replication.
	opts := []grpc.ServerOption{
		grpc.MaxSendMsgSize(config.GRPCMaxSendMsgSize),
		grpc.MaxRecvMsgSize(config.GRPCMaxRecvMsgSize),
	}

	// Initialize marketstore services.
	// --------------------------------
	log.Info("initializing marketstore...")

	// initialize replication master or client
	var rs executor.ReplicationSender
	var grpcReplicationServer *grpc.Server
	if config.Replication.Enabled {
		// Enable TLS for all incoming connections if configured
		if config.Replication.TLSEnabled {
			cert, err2 := tls.LoadX509KeyPair(
				config.Replication.CertFile,
				config.Replication.KeyFile,
			)
			if err2 != nil {
				return fmt.Errorf("failed to load server certificates for replication:"+
					" certFile:%v, keyFile:%v, err:%v",
					config.Replication.CertFile,
					config.Replication.KeyFile,
					err2.Error(),
				)
			}
			opts = append(opts, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
			log.Debug("transport security is enabled on gRPC server for replication")
		}

		grpcReplicationServer = grpc.NewServer(opts...)
		rs, err = initReplicationMaster(globalCtx, grpcReplicationServer, config.Replication.ListenPort)
		if err != nil {
			return fmt.Errorf("failed to initialize replication master: %w", err)
		}
		log.Info("initialized replication master")
	}

	start := time.Now()

	triggerMatchers := trigger.NewTriggerMatchers(config.Triggers)
	instanceConfig, shutdownPending, walWG, err := executor.NewInstanceSetup(
		config.RootDirectory,
		rs,
		triggerMatchers,
		config.WALRotateInterval,
		executor.InitCatalog(config.InitCatalog),
		executor.InitWALCache(config.InitWALCache),
		executor.BackgroundSync(config.BackgroundSync),
		executor.WALBypass(config.WALBypass),
	)
	if err != nil {
		return fmt.Errorf("craete new instance setup: %w", err)
	}

	go metrics.StartDiskUsageMonitor(metrics.TotalDiskUsageBytes, config.RootDirectory, diskUsageMonitorInterval)

	startupTime := time.Since(start)
	metrics.StartupTime.Set(startupTime.Seconds())
	log.Info("startup time: %s", startupTime)

	// Aggregation Functions registry
	aggRunner := sqlparser.NewDefaultAggRunner(instanceConfig.CatalogDir)

	// init QueryService
	qs := frontend.NewQueryService(instanceConfig.CatalogDir)

	// New grpc server for marketstore API.
	grpcServer := grpc.NewServer(
		grpc.MaxSendMsgSize(config.GRPCMaxSendMsgSize),
		grpc.MaxRecvMsgSize(config.GRPCMaxRecvMsgSize),
	)

	// init writer
	var server *frontend.RPCServer
	writer, err := executor.NewWriter(instanceConfig.CatalogDir, instanceConfig.WALFile)
	if err != nil {
		return fmt.Errorf("init writer: %w", err)
	}

	if config.Replication.MasterHost != "" {
		// init replication client
		err = initReplicationClient(
			globalCtx,
			config.Replication.MasterHost,
			config.RootDirectory,
			config.Replication.TLSEnabled,
			config.Replication.CertFile,
			config.Replication.RetryInterval,
			config.Replication.RetryBackoffCoeff,
			writer,
		)
		if err != nil {
			log.Error("Unable to startup Replication", err)
			return err
		}
		log.Info("initialized replication client")

		// New server.
		// WRITE is not allowed on a replica
		errorWriter := &executor.ErrorWriter{}
		server, _ = frontend.NewServer(config.RootDirectory, instanceConfig.CatalogDir, aggRunner, errorWriter, qs)

		// register grpc server
		pb.RegisterMarketstoreServer(grpcServer,
			frontend.NewGRPCService(config.RootDirectory,
				instanceConfig.CatalogDir, aggRunner, errorWriter, qs),
		)
	} else {
		// New server.
		server, _ = frontend.NewServer(config.RootDirectory, instanceConfig.CatalogDir, aggRunner, writer, qs)

		// register grpc server
		pb.RegisterMarketstoreServer(grpcServer,
			frontend.NewGRPCService(config.RootDirectory,
				instanceConfig.CatalogDir, aggRunner, writer, qs),
		)
	}

	// Set rpc handler.
	log.Info("launching rpc data server...")
	http.Handle("/rpc", server)

	// Set websocket handler.
	log.Info("initializing websocket...")
	stream.Initialize()
	http.HandleFunc("/ws", stream.Handler)

	// Set monitoring handler.
	log.Info("launching prometheus metrics server...")
	http.Handle("/metrics", promhttp.Handler())

	// Initialize any provided bgWorker plugins.
	RunBgWorkers(config.BgWorkers)

	if config.UtilitiesURL != "" {
		// Start utility endpoints.
		log.Info("launching utility service...")
		uah := frontend.NewUtilityAPIHandlers(config.StartTime)
		go func() {
			err = uah.Handle(config.UtilitiesURL)
			if err != nil {
				log.Error("utility API handle error: %v", err.Error())
			}
		}()
	}

	log.Info("enabling query access...")
	atomic.StoreUint32(&frontend.Queryable, 1)

	// Serve.
	log.Info("launching tcp listener for all services...")
	if config.GRPCListenURL != "" {
		grpcLn, err2 := net.Listen("tcp", config.GRPCListenURL)
		if err2 != nil {
			return fmt.Errorf("failed to start GRPC server - error: %w", err2)
		}
		go func() {
			err3 := grpcServer.Serve(grpcLn)
			if err3 != nil {
				log.Error("gRPC server error: %v", err.Error())
				grpcServer.GracefulStop()
			}
		}()
	}

	// Spawn a goroutine and listen for a signal.
	const defaultSignalChanLen = 10
	signalChan := make(chan os.Signal, defaultSignalChanLen)
	go func() {
		for s := range signalChan {
			switch s {
			case syscall.SIGUSR1:
				log.Info("dumping stack traces due to SIGUSR1 request")
				err2 := pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
				if err2 != nil {
					log.Error("failed to write goroutine pprof: %w", err)
					return
				}
			case syscall.SIGINT:
				fallthrough
			case syscall.SIGTERM:
				log.Info("initiating graceful shutdown due to '%v' request", s)
				grpcServer.GracefulStop()
				log.Info("shutdown grpc API server...")
				globalCancel()
				if grpcReplicationServer != nil {
					grpcReplicationServer.Stop() // gRPC stream connection doesn't close by GracefulStop()
				}
				log.Info("shutdown grpc Replication server...")

				atomic.StoreUint32(&frontend.Queryable, uint32(0))
				log.Info("waiting a grace period of %v to shutdown...", config.StopGracePeriod)
				time.Sleep(config.StopGracePeriod)
				shutdown(shutdownPending, walWG)
			}
		}
	}()
	signal.Notify(signalChan, syscall.SIGUSR1, syscall.SIGINT, syscall.SIGTERM)

	if err := http.ListenAndServe(config.ListenURL, nil); err != nil {
		return fmt.Errorf("failed to start server - error: %w", err)
	}

	return nil
}

func shutdown(shutdownPending *bool, walWaitGroup *sync.WaitGroup) {
	if shutdownPending != nil {
		*shutdownPending = true
	}
	walWaitGroup.Wait()
	log.Info("exiting...")
	os.Exit(0)
}

func initReplicationMaster(ctx context.Context, grpcServer *grpc.Server, listenPort int) (*replication.Sender, error) {
	grpcReplicationServer := replication.NewGRPCReplicationService()
	pb.RegisterReplicationServer(grpcServer, grpcReplicationServer)

	// start gRPC server for Replication
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", listenPort))
	if err != nil {
		log.Error("failed to listen a port for replication:" + err.Error())
		return nil, fmt.Errorf("failed to listen a port for replication. listenPort=%d:%w", listenPort, err)
	}
	go func() {
		log.Info("starting GRPC server for replication...")
		if err := grpcServer.Serve(lis); err != nil {
			log.Error(fmt.Sprintf("failed to serve replication service:%v", err))
		}
	}()

	replicationSender := replication.NewSender(grpcReplicationServer)
	replicationSender.Run(ctx)

	return replicationSender, nil
}

func initReplicationClient(ctx context.Context, masterHost, rootDir string, tlsEnabled bool, certFile string,
	retryInterval time.Duration, retryBackoffCoeff int, w *executor.Writer) error {
	var opts []grpc.DialOption
	// grpc.WithBlock(),

	if tlsEnabled {
		creds, err := credentials.NewClientTLSFromFile(certFile, "")
		if err != nil {
			return errors.Wrap(err, "failed to load certFile for replication")
		}

		opts = append(opts, grpc.WithTransportCredentials(creds))
		log.Debug("transport security is enabled on gRPC client for replication")
	} else {
		// transport security is disabled
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.Dial(masterHost, opts...)
	if err != nil {
		return errors.Wrap(err, "failed to initialize gRPC client connection for replication")
	}

	c := replication.NewGRPCReplicationClient(pb.NewReplicationClient(conn))

	replayer := replication.NewReplayer(executor.ParseTGData, w.WriteCSM, rootDir)
	replicationReceiver := replication.NewReceiver(c, replayer)

	go func() {
		err = replication.NewRetryer(replicationReceiver.Run, retryInterval, retryBackoffCoeff).Run(ctx)
		if err != nil {
			log.Error("failed to connect Master instance from Replica. err=%v\n", err)
		}
	}()

	return nil
}
