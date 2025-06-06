package networking

import (
	"context"
	"errors"
	"fmt"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/testutils"
	"sync"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	elbv2api "sigs.k8s.io/aws-load-balancer-controller/apis/elbv2/v1beta1"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/k8s"
)

func Test_defaultNetworkingManager_computeIngressPermissionsForTGBNetworking(t *testing.T) {
	port8080 := intstr.FromInt(8080)
	port8443 := intstr.FromInt(8443)
	type args struct {
		tgbNetworking elbv2api.TargetGroupBindingNetworking
		pods          []k8s.PodInfo
	}
	tests := []struct {
		name    string
		args    args
		want    []IPPermissionInfo
		wantErr error
	}{
		{
			name: "with one rule / one peer / one port",
			args: args{
				tgbNetworking: elbv2api.TargetGroupBindingNetworking{
					Ingress: []elbv2api.NetworkingIngressRule{
						{
							From: []elbv2api.NetworkingPeer{
								{
									SecurityGroup: &elbv2api.SecurityGroup{
										GroupID: "sg-abcdefg",
									},
								},
							},
							Ports: []elbv2api.NetworkingPort{
								{
									Port: &port8080,
								},
							},
						},
					},
				},
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "with one rule / multiple peer / multiple port",
			args: args{
				tgbNetworking: elbv2api.TargetGroupBindingNetworking{
					Ingress: []elbv2api.NetworkingIngressRule{
						{
							From: []elbv2api.NetworkingPeer{
								{
									SecurityGroup: &elbv2api.SecurityGroup{
										GroupID: "sg-abcdefg",
									},
								},
								{
									IPBlock: &elbv2api.IPBlock{
										CIDR: "192.168.1.1/16",
									},
								},
							},
							Ports: []elbv2api.NetworkingPort{
								{
									Port: &port8080,
								},
								{
									Port: &port8443,
								},
							},
						},
					},
				},
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8443),
						ToPort:     awssdk.Int32(8443),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						IpRanges: []ec2types.IpRange{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								CidrIp:      awssdk.String("192.168.1.1/16"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8443),
						ToPort:     awssdk.Int32(8443),
						IpRanges: []ec2types.IpRange{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								CidrIp:      awssdk.String("192.168.1.1/16"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "with multiple rule / one peer / one port",
			args: args{
				tgbNetworking: elbv2api.TargetGroupBindingNetworking{
					Ingress: []elbv2api.NetworkingIngressRule{
						{
							From: []elbv2api.NetworkingPeer{
								{
									SecurityGroup: &elbv2api.SecurityGroup{
										GroupID: "sg-abcdefg",
									},
								},
							},
							Ports: []elbv2api.NetworkingPort{
								{
									Port: &port8080,
								},
							},
						},
						{
							From: []elbv2api.NetworkingPeer{
								{
									IPBlock: &elbv2api.IPBlock{
										CIDR: "192.168.1.1/16",
									},
								},
							},
							Ports: []elbv2api.NetworkingPort{
								{
									Port: &port8443,
								},
							},
						},
					},
				},
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8443),
						ToPort:     awssdk.Int32(8443),
						IpRanges: []ec2types.IpRange{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								CidrIp:      awssdk.String("192.168.1.1/16"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{}
			got, err := m.computeIngressPermissionsForTGBNetworking(context.Background(), tt.args.tgbNetworking, tt.args.pods)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_defaultNetworkingManager_computePermissionsForPeerPort(t *testing.T) {
	port8080 := intstr.FromInt(8080)
	portHTTP := intstr.FromString("http")
	protocolUDP := elbv2api.NetworkingProtocolUDP
	type args struct {
		peer elbv2api.NetworkingPeer
		port elbv2api.NetworkingPort
		pods []k8s.PodInfo
	}
	tests := []struct {
		name    string
		args    args
		want    []IPPermissionInfo
		wantErr error
	}{
		{
			name: "permission for securityGroup peer",
			args: args{
				peer: elbv2api.NetworkingPeer{
					SecurityGroup: &elbv2api.SecurityGroup{
						GroupID: "sg-abcdefg",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: &protocolUDP,
					Port:     &port8080,
				},
				pods: nil,
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "permission for IPBlock peer with IPv4 CIDR",
			args: args{
				peer: elbv2api.NetworkingPeer{
					IPBlock: &elbv2api.IPBlock{
						CIDR: "192.168.1.1/16",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: &protocolUDP,
					Port:     &port8080,
				},
				pods: nil,
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						IpRanges: []ec2types.IpRange{
							{
								CidrIp:      awssdk.String("192.168.1.1/16"),
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "permission for IPBlock peer with IPv6 CIDR",
			args: args{
				peer: elbv2api.NetworkingPeer{
					IPBlock: &elbv2api.IPBlock{
						CIDR: "2002::1234:abcd:ffff:c0a8:101/64",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: &protocolUDP,
					Port:     &port8080,
				},
				pods: nil,
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						Ipv6Ranges: []ec2types.Ipv6Range{
							{
								CidrIpv6:    awssdk.String("2002::1234:abcd:ffff:c0a8:101/64"),
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "permission when named ports maps to multiple numeric port",
			args: args{
				peer: elbv2api.NetworkingPeer{
					SecurityGroup: &elbv2api.SecurityGroup{
						GroupID: "sg-abcdefg",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: &protocolUDP,
					Port:     &portHTTP,
				},
				pods: []k8s.PodInfo{
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-1"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 80,
							},
						},
					},
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-2"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 8080,
							},
						},
					},
				},
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(80),
						ToPort:     awssdk.Int32(80),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "permission when protocol defaults to tcp",
			args: args{
				peer: elbv2api.NetworkingPeer{
					SecurityGroup: &elbv2api.SecurityGroup{
						GroupID: "sg-abcdefg",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: nil,
					Port:     &port8080,
				},
				pods: nil,
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("tcp"),
						FromPort:   awssdk.Int32(8080),
						ToPort:     awssdk.Int32(8080),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
		{
			name: "permission when port defaults to all ports",
			args: args{
				peer: elbv2api.NetworkingPeer{
					SecurityGroup: &elbv2api.SecurityGroup{
						GroupID: "sg-abcdefg",
					},
				},
				port: elbv2api.NetworkingPort{
					Protocol: &protocolUDP,
				},
				pods: nil,
			},
			want: []IPPermissionInfo{
				{
					Permission: ec2types.IpPermission{
						IpProtocol: awssdk.String("udp"),
						FromPort:   awssdk.Int32(0),
						ToPort:     awssdk.Int32(65535),
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{
								Description: awssdk.String("elbv2.k8s.aws/targetGroupBinding=shared"),
								GroupId:     awssdk.String("sg-abcdefg"),
							},
						},
					},
					Labels: map[string]string{tgbNetworkingIPPermissionLabelKey: tgbNetworkingIPPermissionLabelValue},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{}
			got, err := m.computePermissionsForPeerPort(context.Background(), tt.args.peer, tt.args.port, tt.args.pods)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_defaultNetworkingManager_computeNumericalPorts(t *testing.T) {
	type args struct {
		port intstr.IntOrString
		pods []k8s.PodInfo
	}
	tests := []struct {
		name    string
		args    args
		want    []int32
		wantErr error
	}{
		{
			name: "numerical port can always be resolved",
			args: args{
				port: intstr.FromInt(8080),
				pods: nil,
			},
			want: []int32{8080},
		},
		{
			name: "named port resolves to same numerical port",
			args: args{
				port: intstr.FromString("http"),
				pods: []k8s.PodInfo{
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-1"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 80,
							},
						},
					},
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-2"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 80,
							},
						},
					},
				},
			},
			want: []int32{80},
		},
		{
			name: "named port resolves to different numerical port",
			args: args{
				port: intstr.FromString("http"),
				pods: []k8s.PodInfo{
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-1"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 80,
							},
						},
					},
					{
						Key: types.NamespacedName{Namespace: "ns-1", Name: "pod-2"},
						ContainerPorts: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 8080,
							},
						},
					},
				},
			},
			want: []int32{80, 8080},
		},
		{
			name: "numerical port cannot be used without pods",
			args: args{
				port: intstr.FromString("http"),
				pods: nil,
			},
			wantErr: errors.New("named ports can only be used with pod endpoints"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{}
			got, err := m.computeNumericalPorts(context.Background(), tt.args.port, tt.args.pods)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_defaultNetworkingManager_computeUnrestrictedIngressPermissionsPerSG(t *testing.T) {
	type fields struct {
		ingressPermissionsPerSGByTGB map[types.NamespacedName]map[string][]IPPermissionInfo
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string][]IPPermissionInfo
	}{
		{
			name: "single tgb",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.169.0.0/16"),
										},
									},
								},
							},
						},
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.169.0.0/16"),
								},
							},
						},
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple tgb",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.169.0.0/16"),
										},
									},
								},
							},
						},
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
						},
					},
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-2"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.170.0.0/16"),
										},
									},
								},
							},
						},
						"sg-c": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.169.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.170.0.0/16"),
								},
							},
						},
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
				},
				"sg-c": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "no tgb",
			fields: fields{
				ingressPermissionsPerSGByTGB: nil,
			},
			want: map[string][]IPPermissionInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{
				ingressPermissionsPerSGByTGB: tt.fields.ingressPermissionsPerSGByTGB,
			}
			got := m.computeUnrestrictedIngressPermissionsPerSG(context.Background())
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_defaultNetworkingManager_computeRestrictedIngressPermissionsPerSG(t *testing.T) {
	type fields struct {
		ingressPermissionsPerSGByTGB map[types.NamespacedName]map[string][]IPPermissionInfo
	}
	var tests = []struct {
		name   string
		fields fields
		want   map[string][]IPPermissionInfo
	}{
		{
			name: "single sg, port not assigned",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   nil,
									ToPort:     nil,
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
			},
		},
		{
			name: "multiple sgs, port not assigned",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   nil,
									ToPort:     nil,
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-2"}: {
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   nil,
									ToPort:     nil,
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-2")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-2")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
			},
		},
		{
			name: "single sg, port range 0 - 65535",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(0),
									ToPort:     awssdk.Int32(65535),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
			},
		},
		{
			name: "multiple sgs, port range 0 - 65535",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(0),
									ToPort:     awssdk.Int32(65535),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-2"}: {
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(0),
									ToPort:     awssdk.Int32(65535),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-2")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(0),
							ToPort:     awssdk.Int32(65535),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-2")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
			},
		},
		{
			name: "single sg, single protocol",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(30),
									ToPort:     awssdk.Int32(8080),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(30),
							ToPort:     awssdk.Int32(8080),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
			},
		},
		{
			name: "multiple sg,  multiple protocols",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(30),
									ToPort:     awssdk.Int32(8080),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("udp"),
									FromPort:   awssdk.Int32(8443),
									ToPort:     awssdk.Int32(8443),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-2")},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("udp"),
									FromPort:   awssdk.Int32(8080),
									ToPort:     awssdk.Int32(8080),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-2")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(30),
							ToPort:     awssdk.Int32(8080),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
						Labels: map[string]string(nil),
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("udp"),
							FromPort:   awssdk.Int32(8080),
							ToPort:     awssdk.Int32(8443),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-2")},
							},
						},
					},
				},
			},
		},
		{
			name: "test for CIDRs",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(80),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(8080),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.169.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(8443),
									ToPort:     awssdk.Int32(8443),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.170.0.0/16"),
										},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(80),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(8080),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.169.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(8443),
							ToPort:     awssdk.Int32(8443),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.170.0.0/16"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "test for both sg and CIDRs",
			fields: fields{
				ingressPermissionsPerSGByTGB: map[types.NamespacedName]map[string][]IPPermissionInfo{
					types.NamespacedName{Namespace: "ns-1", Name: "tgb-1"}: {
						"sg-a": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(80),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.168.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(8080),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.169.0.0/16"),
										},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(80),
									ToPort:     awssdk.Int32(8080),
									IpRanges: []ec2types.IpRange{
										{
											CidrIp: awssdk.String("192.170.0.0/16"),
										},
									},
								},
							},
						},
						"sg-b": {
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(8443),
									ToPort:     awssdk.Int32(9090),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
							{
								Permission: ec2types.IpPermission{
									IpProtocol: awssdk.String("tcp"),
									FromPort:   awssdk.Int32(8443),
									ToPort:     awssdk.Int32(32768),
									UserIdGroupPairs: []ec2types.UserIdGroupPair{
										{GroupId: awssdk.String("group-1")},
									},
								},
							},
						},
					},
				},
			},
			want: map[string][]IPPermissionInfo{
				"sg-a": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(80),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.168.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(8080),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.169.0.0/16"),
								},
							},
						},
					},
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(80),
							ToPort:     awssdk.Int32(8080),
							IpRanges: []ec2types.IpRange{
								{
									CidrIp: awssdk.String("192.170.0.0/16"),
								},
							},
						},
					},
				},
				"sg-b": {
					{
						Permission: ec2types.IpPermission{
							IpProtocol: awssdk.String("tcp"),
							FromPort:   awssdk.Int32(8443),
							ToPort:     awssdk.Int32(32768),
							UserIdGroupPairs: []ec2types.UserIdGroupPair{
								{GroupId: awssdk.String("group-1")},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{
				ingressPermissionsPerSGByTGB: tt.fields.ingressPermissionsPerSGByTGB,
			}
			got := m.computeRestrictedIngressPermissionsPerSG(context.Background())
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_defaultNetworkingManager_resolveEndpointSGForENI(t *testing.T) {
	type fetchSGInfosByIDCall struct {
		req  []string
		resp map[string]SecurityGroupInfo
		err  error
	}
	type fields struct {
		fetchSGInfosByRequestCalls []fetchSGInfosByIDCall
		serviceTargetENISGTags     map[string]string
	}
	type args struct {
		ctx     context.Context
		eniInfo ENIInfo
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr string
	}{
		{
			name: "Only one security group in eniInfo returns early",
			fields: fields{
				serviceTargetENISGTags: map[string]string{},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a"},
				},
			},
			want: "sg-a",
		},
		{
			name: "No security group in eniInfo returns error",
			fields: fields{
				serviceTargetENISGTags: map[string]string{},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req:  []string{},
						resp: map[string]SecurityGroupInfo{},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{},
				},
			},
			want:    "",
			wantErr: "expected exactly one securityGroup tagged with kubernetes.io/cluster/cluster-a for eni eni-a, got: [] (clusterName: cluster-a)",
		},
		{
			name: "A single security group with cluster name tag and no service target tags set",
			fields: fields{
				serviceTargetENISGTags: map[string]string{},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"keyA": "valueA",
									"keyB": "valueB2",
									"keyC": "valueC",
									"keyD": "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want: "sg-a",
		},
		{
			name: "A single security group with cluster name tag and one service target tag set",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyA": "valueA",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "valueA",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want: "sg-b",
		},
		{
			name: "A single security group with cluster name tag and one service target tag set with no matches",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyA": "valueNotA",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "valueA",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want:    "",
			wantErr: "expected exactly one securityGroup tagged with kubernetes.io/cluster/cluster-a and map[keyA:valueNotA] for eni eni-a, got: [] (clusterName: cluster-a)",
		},
		{
			name: "A single security group with cluster name tag and multiple service target tags set",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyA": "valueA",
					"keyB": "valueB2",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "valueA",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want: "sg-b",
		},
		{
			name: "A single security group with cluster name tag and multiple service target tags set with no matches",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyA": "valueA",
					"keyB": "valueNotB2",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "valueA",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want:    "",
			wantErr: "expected exactly one securityGroup tagged with kubernetes.io/cluster/cluster-a and map[keyA:valueA keyB:valueNotB2] for eni eni-a, got: [] (clusterName: cluster-a)",
		},
		{
			name: "A single security group with cluster name tag and a service target tags with an empty value",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyA": "",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want: "sg-b",
		},
		{
			name: "A single security group with cluster name tag and a service target tag with an empty value with no matches",
			fields: fields{
				serviceTargetENISGTags: map[string]string{
					"keyE": "",
				},
				fetchSGInfosByRequestCalls: []fetchSGInfosByIDCall{
					{
						req: []string{"sg-a", "sg-b"},
						resp: map[string]SecurityGroupInfo{
							"sg-a": {
								SecurityGroupID: "sg-a",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
								},
							},
							"sg-b": {
								SecurityGroupID: "sg-b",
								Tags: map[string]string{
									"kubernetes.io/cluster/cluster-a": "owned",
									"keyA":                            "",
									"keyB":                            "valueB2",
									"keyC":                            "valueC",
									"keyD":                            "valueD",
								},
							},
						},
					},
				},
			},
			args: args{
				ctx: context.Background(),
				eniInfo: ENIInfo{
					NetworkInterfaceID: "eni-a",
					SecurityGroups:     []string{"sg-a", "sg-b"},
				},
			},
			want:    "",
			wantErr: "expected exactly one securityGroup tagged with kubernetes.io/cluster/cluster-a and map[keyE:] for eni eni-a, got: [] (clusterName: cluster-a)",
		},
	}
	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		sgManager := NewMockSecurityGroupManager(ctrl)
		for _, call := range tt.fields.fetchSGInfosByRequestCalls {
			sgManager.EXPECT().FetchSGInfosByID(gomock.Any(), call.req).Return(call.resp, call.err)
		}

		t.Run(tt.name, func(t *testing.T) {
			m := &defaultNetworkingManager{
				sgManager:              sgManager,
				clusterName:            "cluster-a",
				serviceTargetENISGTags: tt.fields.serviceTargetENISGTags,
			}
			got, err := m.resolveEndpointSGForENI(tt.args.ctx, tt.args.eniInfo)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_AttemptGarbageCollection(t *testing.T) {
	type fetchSGInfosByRequestCall struct {
		resp map[string]SecurityGroupInfo
		err  error
	}
	vpcId := "vpc-1234"
	clusterName := "cName"
	tcpProtocol := elbv2api.NetworkingProtocolTCP
	tests := []struct {
		name                      string
		tgbsInCluster             []*elbv2api.TargetGroupBinding
		cachedTgbs                []types.NamespacedName
		fetchSGInfosByRequestCall []fetchSGInfosByRequestCall
		expectedSgReconciles      sets.Set[string]
		wantErr                   bool
	}{
		{
			name: "empty cache, no tgb in cluster, empty sg return call",
			fetchSGInfosByRequestCall: []fetchSGInfosByRequestCall{
				{},
			},
		},
		{
			name: "empty cache, no tgb in cluster, sg return call has data",
			fetchSGInfosByRequestCall: []fetchSGInfosByRequestCall{
				{
					resp: map[string]SecurityGroupInfo{
						"sg-a": {
							SecurityGroupID: "sg-a",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
							},
						},
						"sg-b": {
							SecurityGroupID: "sg-b",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
								"keyA":                            "",
								"keyB":                            "valueB2",
								"keyC":                            "valueC",
								"keyD":                            "valueD",
							},
						},
					},
				},
			},
			expectedSgReconciles: sets.Set[string](sets.NewString("sg-a", "sg-b")),
		},
		{
			name: "empty cache, tgb in cluster have no networking config, sg return call has data",
			fetchSGInfosByRequestCall: []fetchSGInfosByRequestCall{
				{
					resp: map[string]SecurityGroupInfo{
						"sg-a": {
							SecurityGroupID: "sg-a",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
							},
						},
						"sg-b": {
							SecurityGroupID: "sg-b",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
								"keyA":                            "",
								"keyB":                            "valueB2",
								"keyC":                            "valueC",
								"keyD":                            "valueD",
							},
						},
					},
				},
			},
			tgbsInCluster: []*elbv2api.TargetGroupBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-a",
						Namespace: "ns-a",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye-fbb5eb7cdd/de68ffdc8cbd5f76",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-b",
						Namespace: "ns-b",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye2-fbb5eb7cdd/de68ffdc8cbd5f76",
					},
				},
			},
			expectedSgReconciles: sets.Set[string](sets.NewString("sg-a", "sg-b")),
		},
		{
			name: "empty cache, tgbs present in cluster, sg return call has data",
			tgbsInCluster: []*elbv2api.TargetGroupBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-a",
						Namespace: "ns-a",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye-fbb5eb7cdd/de68ffdc8cbd5f76",
						Networking: &elbv2api.TargetGroupBindingNetworking{
							Ingress: []elbv2api.NetworkingIngressRule{
								{
									From: []elbv2api.NetworkingPeer{
										{
											SecurityGroup: &elbv2api.SecurityGroup{
												GroupID: "sg-foo",
											},
										},
									},
									Ports: []elbv2api.NetworkingPort{
										{
											Protocol: &tcpProtocol,
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-b",
						Namespace: "ns-b",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye2-fbb5eb7cdd/de68ffdc8cbd5f76",
					},
				},
			},
		},
		{
			name: "cache has other tgb, tgbs present in cluster, sg return call has data",
			tgbsInCluster: []*elbv2api.TargetGroupBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-a",
						Namespace: "ns-a",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye-fbb5eb7cdd/de68ffdc8cbd5f76",
						Networking: &elbv2api.TargetGroupBindingNetworking{
							Ingress: []elbv2api.NetworkingIngressRule{
								{
									From: []elbv2api.NetworkingPeer{
										{
											SecurityGroup: &elbv2api.SecurityGroup{
												GroupID: "sg-foo",
											},
										},
									},
									Ports: []elbv2api.NetworkingPort{
										{
											Protocol: &tcpProtocol,
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-b",
						Namespace: "ns-b",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye2-fbb5eb7cdd/de68ffdc8cbd5f76",
					},
				},
			},
			cachedTgbs: []types.NamespacedName{
				{
					Namespace: "ns-foo",
					Name:      "tgb-foo",
				},
			},
		},
		{
			name: "cache correct, tgbs present in cluster, sg return call has data",
			fetchSGInfosByRequestCall: []fetchSGInfosByRequestCall{
				{
					resp: map[string]SecurityGroupInfo{
						"sg-a": {
							SecurityGroupID: "sg-a",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
							},
						},
						"sg-b": {
							SecurityGroupID: "sg-b",
							Tags: map[string]string{
								"kubernetes.io/cluster/cluster-a": "owned",
								"keyA":                            "",
								"keyB":                            "valueB2",
								"keyC":                            "valueC",
								"keyD":                            "valueD",
							},
						},
					},
				},
			},
			tgbsInCluster: []*elbv2api.TargetGroupBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-a",
						Namespace: "ns-a",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye-fbb5eb7cdd/de68ffdc8cbd5f76",
						Networking: &elbv2api.TargetGroupBindingNetworking{
							Ingress: []elbv2api.NetworkingIngressRule{
								{
									From: []elbv2api.NetworkingPeer{
										{
											SecurityGroup: &elbv2api.SecurityGroup{
												GroupID: "sg-foo",
											},
										},
									},
									Ports: []elbv2api.NetworkingPort{
										{
											Protocol: &tcpProtocol,
										},
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgb-b",
						Namespace: "ns-b",
					},
					Spec: elbv2api.TargetGroupBindingSpec{
						TargetGroupARN: "arn:aws:elasticloadbalancing:us-east-1:565768096483:targetgroup/k8s-servicei-gatewaye2-fbb5eb7cdd/de68ffdc8cbd5f76",
					},
				},
			},
			cachedTgbs: []types.NamespacedName{
				{
					Namespace: "ns-a",
					Name:      "tgb-a",
				},
			},
			expectedSgReconciles: sets.Set[string](sets.NewString("sg-a", "sg-b")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			k8sClient := testutils.GenerateTestClient()

			sgManager := NewMockSecurityGroupManager(ctrl)

			for _, call := range tt.fetchSGInfosByRequestCall {
				sgManager.EXPECT().FetchSGInfosByRequest(gomock.Any(), gomock.Any()).Return(call.resp, call.err)
			}

			for _, tgb := range tt.tgbsInCluster {
				err := k8sClient.Create(context.Background(), tgb)
				assert.NoError(t, err)
			}

			mockReconciler := &mockSGReconciler{}

			m := &defaultNetworkingManager{
				sgManager:                    sgManager,
				k8sClient:                    k8sClient,
				clusterName:                  clusterName,
				vpcID:                        vpcId,
				mutex:                        sync.Mutex{},
				ingressPermissionsPerSGByTGB: make(map[types.NamespacedName]map[string][]IPPermissionInfo),
				trackedEndpointSGs:           sets.NewString(),
				sgReconciler:                 mockReconciler,
			}

			for _, nsn := range tt.cachedTgbs {
				m.ingressPermissionsPerSGByTGB[nsn] = make(map[string][]IPPermissionInfo)
			}
			err := m.AttemptGarbageCollection(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tt.expectedSgReconciles), len(mockReconciler.calls))
				for _, call := range mockReconciler.calls {
					assert.True(t, tt.expectedSgReconciles.Has(call.sgID), fmt.Sprintf("expected sgID: %s to be in calls", call.sgID))
				}
			}
		})
	}
}

type reconcileIngressCall struct {
	sgID               string
	desiredPermissions []IPPermissionInfo
	opts               []SecurityGroupReconcileOption
}
type mockSGReconciler struct {
	calls []reconcileIngressCall
}

func (m *mockSGReconciler) ReconcileIngress(ctx context.Context, sgID string, desiredPermissions []IPPermissionInfo, opts ...SecurityGroupReconcileOption) error {
	m.calls = append(m.calls, reconcileIngressCall{
		sgID:               sgID,
		desiredPermissions: desiredPermissions,
		opts:               opts,
	})
	return nil
}
