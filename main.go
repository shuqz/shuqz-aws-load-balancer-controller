/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"
	"sigs.k8s.io/aws-load-balancer-controller/controllers/gateway"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/gateway/routeutils"

	"k8s.io/client-go/util/workqueue"

	elbv2deploy "sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/elbv2"

	"github.com/go-logr/logr"
	"github.com/spf13/pflag"
	zapraw "go.uber.org/zap"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog/v2"
	elbv2api "sigs.k8s.io/aws-load-balancer-controller/apis/elbv2/v1beta1"
	elbv2controller "sigs.k8s.io/aws-load-balancer-controller/controllers/elbv2"
	"sigs.k8s.io/aws-load-balancer-controller/controllers/ingress"
	"sigs.k8s.io/aws-load-balancer-controller/controllers/service"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/aws"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/aws/throttle"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/config"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/inject"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/k8s"
	awsmetrics "sigs.k8s.io/aws-load-balancer-controller/pkg/metrics/aws"
	lbcmetrics "sigs.k8s.io/aws-load-balancer-controller/pkg/metrics/lbc"
	metricsutil "sigs.k8s.io/aws-load-balancer-controller/pkg/metrics/util"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/networking"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/runtime"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/targetgroupbinding"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/version"
	corewebhook "sigs.k8s.io/aws-load-balancer-controller/webhooks/core"
	elbv2webhook "sigs.k8s.io/aws-load-balancer-controller/webhooks/elbv2"
	networkingwebhook "sigs.k8s.io/aws-load-balancer-controller/webhooks/networking"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = k8sruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = elbv2api.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	infoLogger := getLoggerWithLogLevel("info")
	infoLogger.Info("version",
		"GitVersion", version.GitVersion,
		"GitCommit", version.GitCommit,
		"BuildDate", version.BuildDate,
	)
	controllerCFG, err := loadControllerConfig()
	if err != nil {
		infoLogger.Error(err, "unable to load controller config")
		os.Exit(1)
	}
	appLogger := getLoggerWithLogLevel(controllerCFG.LogLevel)
	ctrl.SetLogger(appLogger)
	klog.SetLoggerWithOptions(appLogger, klog.ContextualLogger(true))

	var awsMetricsCollector *awsmetrics.Collector

	if metrics.Registry != nil {
		awsMetricsCollector = awsmetrics.NewCollector(metrics.Registry)
	}

	cloud, err := aws.NewCloud(controllerCFG.AWSConfig, controllerCFG.ClusterName, awsMetricsCollector, ctrl.Log, nil)
	if err != nil {
		setupLog.Error(err, "unable to initialize AWS cloud")
		os.Exit(1)
	}
	restCFG, err := config.BuildRestConfig(controllerCFG.RuntimeConfig)
	if err != nil {
		setupLog.Error(err, "unable to build REST config")
		os.Exit(1)
	}
	rtOpts := config.BuildRuntimeOptions(controllerCFG.RuntimeConfig, scheme)
	mgr, err := ctrl.NewManager(restCFG, rtOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	reconcileCounters := metricsutil.NewReconcileCounters()
	lbcMetricsCollector := lbcmetrics.NewCollector(metrics.Registry, mgr, reconcileCounters, ctrl.Log.WithName("controller_metrics"))

	clientSet, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to obtain clientSet")
		os.Exit(1)
	}

	podInfoRepo := k8s.NewDefaultPodInfoRepo(clientSet.CoreV1().RESTClient(), controllerCFG.RuntimeConfig.WatchNamespace, ctrl.Log)
	finalizerManager := k8s.NewDefaultFinalizerManager(mgr.GetClient(), ctrl.Log)
	sgManager := networking.NewDefaultSecurityGroupManager(cloud.EC2(), ctrl.Log)
	sgReconciler := networking.NewDefaultSecurityGroupReconciler(sgManager, ctrl.Log)
	azInfoProvider := networking.NewDefaultAZInfoProvider(cloud.EC2(), ctrl.Log.WithName("az-info-provider"))
	vpcInfoProvider := networking.NewDefaultVPCInfoProvider(cloud.EC2(), ctrl.Log.WithName("vpc-info-provider"))
	subnetResolver := networking.NewDefaultSubnetsResolver(azInfoProvider, cloud.EC2(), cloud.VpcID(), controllerCFG.ClusterName,
		controllerCFG.FeatureGates.Enabled(config.SubnetsClusterTagCheck),
		controllerCFG.FeatureGates.Enabled(config.ALBSingleSubnet),
		controllerCFG.FeatureGates.Enabled(config.SubnetDiscoveryByReachability),
		ctrl.Log.WithName("subnets-resolver"))
	multiClusterManager := targetgroupbinding.NewMultiClusterManager(mgr.GetClient(), mgr.GetAPIReader(), ctrl.Log)
	tgbResManager := targetgroupbinding.NewDefaultResourceManager(mgr.GetClient(), cloud.ELBV2(), cloud.EC2(),
		podInfoRepo, sgManager, sgReconciler, vpcInfoProvider, multiClusterManager, lbcMetricsCollector,
		cloud.VpcID(), controllerCFG.ClusterName, controllerCFG.FeatureGates.Enabled(config.EndpointsFailOpen), controllerCFG.EnableEndpointSlices, controllerCFG.DisableRestrictedSGRules,
		controllerCFG.ServiceTargetENISGTags, mgr.GetEventRecorderFor("targetGroupBinding"), ctrl.Log)
	backendSGProvider := networking.NewBackendSGProvider(controllerCFG.ClusterName, controllerCFG.BackendSecurityGroup,
		cloud.VpcID(), cloud.EC2(), mgr.GetClient(), controllerCFG.DefaultTags, ctrl.Log.WithName("backend-sg-provider"))
	sgResolver := networking.NewDefaultSecurityGroupResolver(cloud.EC2(), cloud.VpcID())
	elbv2TaggingManager := elbv2deploy.NewDefaultTaggingManager(cloud.ELBV2(), cloud.VpcID(), controllerCFG.FeatureGates, cloud.RGT(), ctrl.Log)
	ingGroupReconciler := ingress.NewGroupReconciler(cloud, mgr.GetClient(), mgr.GetEventRecorderFor("ingress"),
		finalizerManager, sgManager, sgReconciler, subnetResolver, elbv2TaggingManager,
		controllerCFG, backendSGProvider, sgResolver, ctrl.Log.WithName("controllers").WithName("ingress"), lbcMetricsCollector, reconcileCounters)
	svcReconciler := service.NewServiceReconciler(cloud, mgr.GetClient(), mgr.GetEventRecorderFor("service"),
		finalizerManager, sgManager, sgReconciler, subnetResolver, vpcInfoProvider, elbv2TaggingManager,
		controllerCFG, backendSGProvider, sgResolver, ctrl.Log.WithName("controllers").WithName("service"), lbcMetricsCollector, reconcileCounters)

	delayingQueue := workqueue.NewDelayingQueueWithConfig(workqueue.DelayingQueueConfig{
		Name: "delayed-target-group-binding",
	})

	deferredTGBQueue := elbv2controller.NewDeferredTargetGroupBindingReconciler(delayingQueue, controllerCFG.RuntimeConfig.SyncPeriod, mgr.GetClient(), ctrl.Log.WithName("deferredTGBQueue"))
	tgbReconciler := elbv2controller.NewTargetGroupBindingReconciler(mgr.GetClient(), mgr.GetEventRecorderFor("targetGroupBinding"),
		finalizerManager, tgbResManager, controllerCFG, deferredTGBQueue, ctrl.Log.WithName("controllers").WithName("targetGroupBinding"), lbcMetricsCollector, reconcileCounters)

	ctx := ctrl.SetupSignalHandler()
	if err = ingGroupReconciler.SetupWithManager(ctx, mgr, clientSet); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Ingress")
		os.Exit(1)
	}

	// Setup service reconciler only if AllowServiceType is set to true.
	if controllerCFG.FeatureGates.Enabled(config.EnableServiceController) {
		if err = svcReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "Unable to create controller", "controller", "Service")
			os.Exit(1)
		}
	}

	if err := tgbReconciler.SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TargetGroupBinding")
		os.Exit(1)
	}

	var routeLoader routeutils.Loader
	if controllerCFG.FeatureGates.Enabled(config.NLBGatewayAPI) {
		routeLoader = routeutils.NewLoader(mgr.GetClient())
		nlbGatewayReconciler := gateway.NewNLBGatewayReconciler(routeLoader, cloud, mgr.GetClient(), mgr.GetEventRecorderFor("nlbgateway"), controllerCFG, finalizerManager, sgReconciler, sgManager, elbv2TaggingManager, subnetResolver, vpcInfoProvider, backendSGProvider, sgResolver, ctrl.Log.WithName("controllers").WithName("nlbgateway"), lbcMetricsCollector, reconcileCounters)
		nlbControllerError := nlbGatewayReconciler.SetupWithManager(mgr)
		if nlbControllerError != nil {
			setupLog.Error(nlbControllerError, "Unable to create NLB Gateway controller")
			os.Exit(1)
		}
	}

	if controllerCFG.FeatureGates.Enabled(config.ALBGatewayAPI) {
		if routeLoader == nil {
			routeLoader = routeutils.NewLoader(mgr.GetClient())
		}
		albGatewayReconciler := gateway.NewALBGatewayReconciler(routeLoader, cloud, mgr.GetClient(), mgr.GetEventRecorderFor("albgateway"), controllerCFG, finalizerManager, sgReconciler, sgManager, elbv2TaggingManager, subnetResolver, vpcInfoProvider, backendSGProvider, sgResolver, ctrl.Log.WithName("controllers").WithName("albgateway"), lbcMetricsCollector, reconcileCounters)
		albControllerErr := albGatewayReconciler.SetupWithManager(mgr)
		if albControllerErr != nil {
			setupLog.Error(albControllerErr, "Unable to create ALB Gateway controller")
			os.Exit(1)
		}
	}

	// Add liveness probe
	err = mgr.AddHealthzCheck("health-ping", healthz.Ping)
	setupLog.Info("adding health check for controller")
	if err != nil {
		setupLog.Error(err, "unable add a health check")
		os.Exit(1)
	}

	// Add readiness probe
	err = mgr.AddReadyzCheck("ready-webhook", mgr.GetWebhookServer().StartedChecker())
	setupLog.Info("adding readiness check for webhook")
	if err != nil {
		setupLog.Error(err, "unable add a readiness check")
		os.Exit(1)
	}

	podReadinessGateInjector := inject.NewPodReadinessGate(controllerCFG.PodWebhookConfig,
		mgr.GetClient(), ctrl.Log.WithName("pod-readiness-gate-injector"))
	corewebhook.NewPodMutator(podReadinessGateInjector, lbcMetricsCollector).SetupWithManager(mgr)
	corewebhook.NewServiceMutator(controllerCFG.ServiceConfig.LoadBalancerClass, ctrl.Log, lbcMetricsCollector).SetupWithManager(mgr)
	elbv2webhook.NewIngressClassParamsValidator(lbcMetricsCollector).SetupWithManager(mgr)
	elbv2webhook.NewTargetGroupBindingMutator(cloud.ELBV2(), ctrl.Log, lbcMetricsCollector).SetupWithManager(mgr)
	elbv2webhook.NewTargetGroupBindingValidator(mgr.GetClient(), cloud.ELBV2(), cloud.VpcID(), ctrl.Log, lbcMetricsCollector).SetupWithManager(mgr)
	networkingwebhook.NewIngressValidator(mgr.GetClient(), controllerCFG.IngressConfig, ctrl.Log, lbcMetricsCollector).SetupWithManager(mgr)
	//+kubebuilder:scaffold:builder

	go func() {
		setupLog.Info("starting podInfo repo")
		if err := podInfoRepo.Start(ctx); err != nil {
			setupLog.Error(err, "problem running podInfo repo")
			os.Exit(1)
		}
	}()

	go func() {
		setupLog.Info("starting deferred tgb reconciler")
		deferredTGBQueue.Run()
	}()

	if err := podInfoRepo.WaitForCacheSync(ctx); err != nil {
		setupLog.Error(err, "problem wait for podInfo repo sync")
		os.Exit(1)
	}

	go func() {
		setupLog.Info("starting collect cache size")
		lbcMetricsCollector.StartCollectCacheSize(ctx)
	}()

	go func() {
		setupLog.Info("starting collect top talkers")
		lbcMetricsCollector.StartCollectTopTalkers(ctx)
	}()

	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// loadControllerConfig loads the controller configuration.
func loadControllerConfig() (config.ControllerConfig, error) {
	defaultAWSThrottleCFG := throttle.NewDefaultServiceOperationsThrottleConfig()
	controllerCFG := config.ControllerConfig{
		AWSConfig: aws.CloudConfig{
			ThrottleConfig: defaultAWSThrottleCFG,
		},
		FeatureGates: config.NewFeatureGates(),
	}

	fs := pflag.NewFlagSet("", pflag.ExitOnError)
	controllerCFG.BindFlags(fs)

	if err := fs.Parse(os.Args); err != nil {
		return config.ControllerConfig{}, err
	}

	if err := controllerCFG.Validate(); err != nil {
		return config.ControllerConfig{}, err
	}
	return controllerCFG, nil
}

// getLoggerWithLogLevel returns logger with specific log level.
func getLoggerWithLogLevel(logLevel string) logr.Logger {
	var zapLevel zapraw.AtomicLevel
	switch logLevel {
	case "info":
		zapLevel = zapraw.NewAtomicLevelAt(zapraw.InfoLevel)
	case "debug":
		zapLevel = zapraw.NewAtomicLevelAt(zapraw.DebugLevel)
	default:
		zapLevel = zapraw.NewAtomicLevelAt(zapraw.InfoLevel)
	}

	logger := zap.New(zap.UseDevMode(false),
		zap.Level(zapLevel),
		zap.StacktraceLevel(zapraw.NewAtomicLevelAt(zapraw.FatalLevel)))
	return runtime.NewConciseLogger(logger)
}
