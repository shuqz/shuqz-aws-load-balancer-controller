package deploy

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/aws/services"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/config"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/ec2"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/elbv2"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/shield"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/tracking"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/wafregional"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/deploy/wafv2"
	errmetrics "sigs.k8s.io/aws-load-balancer-controller/pkg/error"
	lbcmetrics "sigs.k8s.io/aws-load-balancer-controller/pkg/metrics/lbc"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/model/core"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/networking"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ingressController = "ingress"
)

// Using elbv2.TargetGroupsResult instead of defining our own

// StackDeployer will deploy a resource stack into AWS and K8S.
type StackDeployer interface {
	// Deploy a resource stack.
	Deploy(ctx context.Context, stack core.Stack, metricsCollector lbcmetrics.MetricCollector, controllerName string, frontendNlbTargetGroupDesiredState *core.FrontendNlbTargetGroupDesiredState) error
}

// NewDefaultStackDeployer constructs new defaultStackDeployer.
func NewDefaultStackDeployer(cloud services.Cloud, k8sClient client.Client,
	networkingManager networking.NetworkingManager, networkingSGManager networking.SecurityGroupManager, networkingSGReconciler networking.SecurityGroupReconciler,
	elbv2TaggingManager elbv2.TaggingManager,
	config config.ControllerConfig, tagPrefix string, logger logr.Logger, metricsCollector lbcmetrics.MetricCollector, controllerName string) *defaultStackDeployer {

	trackingProvider := tracking.NewDefaultProvider(tagPrefix, config.ClusterName)
	ec2TaggingManager := ec2.NewDefaultTaggingManager(cloud.EC2(), networkingSGManager, cloud.VpcID(), logger)

	return &defaultStackDeployer{
		cloud:                               cloud,
		k8sClient:                           k8sClient,
		controllerConfig:                    config,
		addonsConfig:                        config.AddonsConfig,
		trackingProvider:                    trackingProvider,
		ec2TaggingManager:                   ec2TaggingManager,
		ec2SGManager:                        ec2.NewDefaultSecurityGroupManager(cloud.EC2(), networkingManager, trackingProvider, ec2TaggingManager, networkingSGReconciler, cloud.VpcID(), config.ExternalManagedTags, logger),
		elbv2TaggingManager:                 elbv2TaggingManager,
		elbv2LBManager:                      elbv2.NewDefaultLoadBalancerManager(cloud.ELBV2(), trackingProvider, elbv2TaggingManager, config.ExternalManagedTags, config.FeatureGates, logger),
		elbv2LSManager:                      elbv2.NewDefaultListenerManager(cloud.ELBV2(), trackingProvider, elbv2TaggingManager, config.ExternalManagedTags, config.FeatureGates, logger),
		elbv2LRManager:                      elbv2.NewDefaultListenerRuleManager(cloud.ELBV2(), trackingProvider, elbv2TaggingManager, config.ExternalManagedTags, config.FeatureGates, logger),
		elbv2TGManager:                      elbv2.NewDefaultTargetGroupManager(cloud.ELBV2(), trackingProvider, elbv2TaggingManager, cloud.VpcID(), config.ExternalManagedTags, logger),
		elbv2TGBManager:                     elbv2.NewDefaultTargetGroupBindingManager(k8sClient, trackingProvider, logger),
		elbv2FrontendNlbTargetsManager:      elbv2.NewFrontendNlbTargetsManager(cloud.ELBV2(), logger),
		wafv2WebACLAssociationManager:       wafv2.NewDefaultWebACLAssociationManager(cloud.WAFv2(), logger),
		wafRegionalWebACLAssociationManager: wafregional.NewDefaultWebACLAssociationManager(cloud.WAFRegional(), logger),
		shieldProtectionManager:             shield.NewDefaultProtectionManager(cloud.Shield(), logger),
		featureGates:                        config.FeatureGates,
		vpcID:                               cloud.VpcID(),
		logger:                              logger,
		metricsCollector:                    metricsCollector,
		controllerName:                      controllerName,
	}
}

var _ StackDeployer = &defaultStackDeployer{}

// defaultStackDeployer is the default implementation for StackDeployer
type defaultStackDeployer struct {
	cloud                               services.Cloud
	k8sClient                           client.Client
	controllerConfig                    config.ControllerConfig
	addonsConfig                        config.AddonsConfig
	trackingProvider                    tracking.Provider
	ec2TaggingManager                   ec2.TaggingManager
	ec2SGManager                        ec2.SecurityGroupManager
	elbv2TaggingManager                 elbv2.TaggingManager
	elbv2LBManager                      elbv2.LoadBalancerManager
	elbv2LSManager                      elbv2.ListenerManager
	elbv2LRManager                      elbv2.ListenerRuleManager
	elbv2TGManager                      elbv2.TargetGroupManager
	elbv2TGBManager                     elbv2.TargetGroupBindingManager
	elbv2FrontendNlbTargetsManager      elbv2.FrontendNlbTargetsManager
	wafv2WebACLAssociationManager       wafv2.WebACLAssociationManager
	wafRegionalWebACLAssociationManager wafregional.WebACLAssociationManager
	shieldProtectionManager             shield.ProtectionManager
	featureGates                        config.FeatureGates
	vpcID                               string
	metricsCollector                    lbcmetrics.MetricCollector
	controllerName                      string

	logger logr.Logger
}

type ResourceSynthesizer interface {
	Synthesize(ctx context.Context) error
	PostSynthesize(ctx context.Context) error
}

// Deploy a resource stack.
func (d *defaultStackDeployer) Deploy(ctx context.Context, stack core.Stack, metricsCollector lbcmetrics.MetricCollector, controllerName string, frontendNlbTargetGroupDesiredState *core.FrontendNlbTargetGroupDesiredState) error {
	synthesizers := []ResourceSynthesizer{
		ec2.NewSecurityGroupSynthesizer(d.cloud.EC2(), d.trackingProvider, d.ec2TaggingManager, d.ec2SGManager, d.vpcID, d.logger, stack),
	}

	// Create a cached function that will only execute once to fetch target groups
	// This is to avoid duplicate ListTargetGroups API call
	findSDKTargetGroups := sync.OnceValue(func() elbv2.TargetGroupsResult {
		stackTags := d.trackingProvider.StackTags(stack)
		stackTagsLegacy := d.trackingProvider.StackTagsLegacy(stack)
		tgs, err := d.elbv2TaggingManager.ListTargetGroups(ctx,
			tracking.TagsAsTagFilter(stackTags),
			tracking.TagsAsTagFilter(stackTagsLegacy))
		return elbv2.TargetGroupsResult{TargetGroups: tgs, Err: err}
	})

	if controllerName == ingressController {
		synthesizers = append(synthesizers, elbv2.NewFrontendNlbTargetSynthesizer(
			d.k8sClient, d.trackingProvider, d.elbv2TaggingManager, d.elbv2FrontendNlbTargetsManager, d.logger, d.featureGates, stack, frontendNlbTargetGroupDesiredState, findSDKTargetGroups))
	}

	synthesizers = append(synthesizers,
		elbv2.NewTargetGroupSynthesizer(d.cloud.ELBV2(), d.trackingProvider, d.elbv2TaggingManager, d.elbv2TGManager, d.logger, d.featureGates, stack, findSDKTargetGroups),
		elbv2.NewLoadBalancerSynthesizer(d.cloud.ELBV2(), d.trackingProvider, d.elbv2TaggingManager, d.elbv2LBManager, d.logger, d.featureGates, d.controllerConfig, stack),
		elbv2.NewListenerSynthesizer(d.cloud.ELBV2(), d.elbv2TaggingManager, d.elbv2LSManager, d.logger, stack),
		elbv2.NewListenerRuleSynthesizer(d.cloud.ELBV2(), d.elbv2TaggingManager, d.elbv2LRManager, d.logger, d.featureGates, stack),
		elbv2.NewTargetGroupBindingSynthesizer(d.k8sClient, d.trackingProvider, d.elbv2TGBManager, d.logger, stack))

	if d.addonsConfig.WAFV2Enabled {
		synthesizers = append(synthesizers, wafv2.NewWebACLAssociationSynthesizer(d.wafv2WebACLAssociationManager, d.logger, stack))
	}
	if d.addonsConfig.WAFEnabled && d.cloud.WAFRegional().Available() {
		synthesizers = append(synthesizers, wafregional.NewWebACLAssociationSynthesizer(d.wafRegionalWebACLAssociationManager, d.logger, stack))
	}
	if d.addonsConfig.ShieldEnabled {
		shieldSubscribed, err := d.shieldProtectionManager.IsSubscribed(ctx)
		if err != nil {
			d.logger.Error(err, "unable to determine AWS Shield subscription state, skipping AWS shield reconciliation")
		} else if shieldSubscribed {
			synthesizers = append(synthesizers, shield.NewProtectionSynthesizer(d.shieldProtectionManager, d.logger, stack))
		}
	}

	for _, synthesizer := range synthesizers {
		var err error
		// Get synthesizer type name for better context
		synthesizerType := fmt.Sprintf("%T", synthesizer)
		synthesizeFn := func() {
			err = synthesizer.Synthesize(ctx)
		}
		d.metricsCollector.ObserveControllerReconcileLatency(controllerName, synthesizerType, synthesizeFn)
		if err != nil {
			return errmetrics.NewErrorWithMetrics(controllerName, synthesizerType, err, d.metricsCollector)
		}
	}
	for i := len(synthesizers) - 1; i >= 0; i-- {
		if err := synthesizers[i].PostSynthesize(ctx); err != nil {
			return err
		}
	}

	return nil
}
