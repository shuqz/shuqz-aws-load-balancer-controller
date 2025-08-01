package routeutils

import (
	"context"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/gateway/constants"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	"time"
)

/*
This class holds the representation of a GRPC route.
Generally, outside consumers will use GetRawRouteRule to inspect the
GRPC specific features of the route.
*/

/* Route Rule */

var _ RouteRule = &convertedGRPCRouteRule{}

var defaultGRPCRuleAccumulator = newAttachedRuleAccumulator[gwv1.GRPCRouteRule](commonBackendLoader)

type convertedGRPCRouteRule struct {
	rule     *gwv1.GRPCRouteRule
	backends []Backend
}

func (t *convertedGRPCRouteRule) GetRawRouteRule() interface{} {
	return t.rule
}

func convertGRPCRouteRule(rule *gwv1.GRPCRouteRule, backends []Backend) RouteRule {
	return &convertedGRPCRouteRule{
		rule:     rule,
		backends: backends,
	}
}

func (t *convertedGRPCRouteRule) GetSectionName() *gwv1.SectionName {
	return t.rule.Name
}

func (t *convertedGRPCRouteRule) GetBackends() []Backend {
	return t.backends
}

/* Route Description */

type grpcRouteDescription struct {
	route           *gwv1.GRPCRoute
	rules           []RouteRule
	ruleAccumulator attachedRuleAccumulator[gwv1.GRPCRouteRule]
}

func (grpcRoute *grpcRouteDescription) loadAttachedRules(ctx context.Context, k8sClient client.Client) (RouteDescriptor, []routeLoadError) {
	convertedRules, allErrors := grpcRoute.ruleAccumulator.accumulateRules(ctx, k8sClient, grpcRoute, grpcRoute.route.Spec.Rules, func(rule gwv1.GRPCRouteRule) []gwv1.BackendRef {
		refs := make([]gwv1.BackendRef, 0, len(rule.BackendRefs))
		for _, grpcRef := range rule.BackendRefs {
			refs = append(refs, grpcRef.BackendRef)
		}
		return refs
	}, func(grr *gwv1.GRPCRouteRule, backends []Backend) RouteRule {
		return convertGRPCRouteRule(grr, backends)
	})
	grpcRoute.rules = convertedRules
	return grpcRoute, allErrors
}

func (grpcRoute *grpcRouteDescription) GetHostnames() []gwv1.Hostname {
	return grpcRoute.route.Spec.Hostnames
}

func (grpcRoute *grpcRouteDescription) GetAttachedRules() []RouteRule {
	return grpcRoute.rules
}

func (grpcRoute *grpcRouteDescription) GetParentRefs() []gwv1.ParentReference {
	return grpcRoute.route.Spec.ParentRefs
}

func (grpcRoute *grpcRouteDescription) GetRouteKind() RouteKind {
	return GRPCRouteKind
}

func (grpcRoute *grpcRouteDescription) GetRouteNamespacedName() types.NamespacedName {
	return k8s.NamespacedName(grpcRoute.route)
}

func convertGRPCRoute(r gwv1.GRPCRoute) *grpcRouteDescription {
	return &grpcRouteDescription{route: &r, ruleAccumulator: defaultGRPCRuleAccumulator}
}

func (grpcRoute *grpcRouteDescription) GetRawRoute() interface{} {
	return grpcRoute.route
}

func (grpcRoute *grpcRouteDescription) GetBackendRefs() []gwv1.BackendRef {
	backendRefs := make([]gwv1.BackendRef, 0)
	if grpcRoute.route.Spec.Rules != nil {
		for _, rule := range grpcRoute.route.Spec.Rules {
			for _, grpcBackendRef := range rule.BackendRefs {
				backendRefs = append(backendRefs, grpcBackendRef.BackendRef)
			}
		}
	}
	return backendRefs
}

// GetListenerRuleConfigs returns all ListenerRuleConfiguration references from
// ExtensionRef filters in the GRPCRoute
func (grpcRoute *grpcRouteDescription) GetListenerRuleConfigs() []gwv1.LocalObjectReference {
	listenerRuleConfigs := make([]gwv1.LocalObjectReference, 0)
	if grpcRoute.route.Spec.Rules != nil {
		for _, rule := range grpcRoute.route.Spec.Rules {
			if rule.Filters != nil {
				for _, filter := range rule.Filters {
					if filter.Type != gwv1.GRPCRouteFilterExtensionRef {
						continue
					}
					if string(filter.ExtensionRef.Group) == constants.ControllerCRDGroupVersion && string(filter.ExtensionRef.Kind) == constants.ListenerRuleConfiguration {
						listenerRuleConfigs = append(listenerRuleConfigs, gwv1.LocalObjectReference{
							Group: constants.ControllerCRDGroupVersion,
							Kind:  constants.ListenerRuleConfiguration,
							Name:  filter.ExtensionRef.Name,
						})
					}
				}

			}
		}
	}
	return listenerRuleConfigs
}

func (grpcRoute *grpcRouteDescription) GetRouteGeneration() int64 {
	return grpcRoute.route.Generation
}

func (grpcRoute *grpcRouteDescription) GetRouteCreateTimestamp() time.Time {
	return grpcRoute.route.CreationTimestamp.Time
}

var _ RouteDescriptor = &grpcRouteDescription{}

// Can we use an indexer here to query more efficiently?

func ListGRPCRoutes(context context.Context, k8sClient client.Client, opts ...client.ListOption) ([]preLoadRouteDescriptor, error) {
	routeList := &gwv1.GRPCRouteList{}
	err := k8sClient.List(context, routeList, opts...)
	if err != nil {
		return nil, err
	}

	result := make([]preLoadRouteDescriptor, 0)

	for _, item := range routeList.Items {
		result = append(result, convertGRPCRoute(item))
	}

	return result, nil
}
