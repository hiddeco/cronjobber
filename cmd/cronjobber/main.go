package main

import (
	"flag"
	"k8s.io/klog/v2"
	"log"
	"time"
	_ "time/tzdata"

	clientset "github.com/hiddeco/cronjobber/pkg/client/clientset/versioned"
	informers "github.com/hiddeco/cronjobber/pkg/client/informers/externalversions"
	"github.com/hiddeco/cronjobber/pkg/controller/cronjobber"
	"github.com/hiddeco/cronjobber/pkg/logging"
	"github.com/hiddeco/cronjobber/pkg/version"
	"knative.dev/pkg/signals"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	masterURL  string
	kubeconfig string
	logLevel   string
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&logLevel, "log-level", "info", "One of: debug, info, warn, error, fatal, panic.")
}

func main() {
	// Ensure klog flags are initialized so they can be updated
	klog.InitFlags(nil)

	// Ensure klog logs to stderr, as it will otherwise try to
	// write to `/tmp` that does not exist in scratch images
	flag.Set("logtostderr", "true")

	// Parse flags
	flag.Parse()

	// Initialize logger
	logger, err := logging.NewLogger(logLevel)
	if err != nil {
		log.Fatalf("Error creating logger: %v", err)
	}
	defer logger.Sync()

	// Build kubeconfig
	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		logger.Fatalf("Error building kubeconfig: %v", err)
	}

	// Setup client-go and ensure healthy connection to API
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building Kubernetes clientset: %v", err)
	}
	ver, err := kubeClient.Discovery().ServerVersion()
	if err != nil {
		logger.Fatalf("Error calling Kubernetes API: %v", err)
	}
	logger.Infof("Connected to Kubernetes API: %s", ver)

	// Setup cronjobber
	cronjobberClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building Cronjobber clientset: %v", err)
	}

	cronjobberInformerFactory := informers.NewSharedInformerFactory(cronjobberClient, 30*time.Second)
	cjInformer := cronjobberInformerFactory.Cronjobber().V1alpha1().TZCronJobs()

	logger.Infof("Starting Cronjobber version %s revision %s", version.VERSION, version.REVISION)

	cm, err := cronjobber.NewTZCronJobController(kubeClient, cronjobberClient, cjInformer, logger)
	if err != nil {
		logger.Fatalf("Error building TZCronJobController: %v", err)
	}

	stopCh := signals.SetupSignalHandler()

	cronjobberInformerFactory.Start(stopCh)
	cm.Run(stopCh)

	<-stopCh
}
