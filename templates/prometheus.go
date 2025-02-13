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

package templates

import (
	"fmt"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/red-hat-storage/mcg-osd-deployer/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	KubeRBACProxyPortNumber             = 9339
	PrometheusServingCertSecretName     = "prometheus-serving-cert-secret"
	PrometheusKubeRBACPoxyConfigMapName = "prometheus-kube-rbac-proxy-config"
)

// PrometheusTemplate is the template that serves as the base for the prometheus deployed by the operator.
var resourceSelector = metav1.LabelSelector{
	MatchLabels: map[string]string{
		"app": "managed-mcg",
	},
}

var PrometheusTemplate = promv1.Prometheus{
	Spec: promv1.PrometheusSpec{
		ServiceAccountName:     "prometheus-k8s",
		ServiceMonitorSelector: &resourceSelector,
		PodMonitorSelector:     &resourceSelector,
		RuleSelector:           &resourceSelector,
		EnableAdminAPI:         false,
		Alerting: &promv1.AlertingSpec{
			Alertmanagers: []promv1.AlertmanagerEndpoints{{
				Namespace: "",
				Name:      "alertmanager-operated",
				Port:      intstr.FromString("web"),
			}},
		},
		Resources:   utils.GetResourceRequirements("prometheus"),
		ListenLocal: true,
		Containers: []corev1.Container{{
			Name: "kube-rbac-proxy",
			Args: []string{
				fmt.Sprintf("--secure-listen-address=0.0.0.0:%d", KubeRBACProxyPortNumber),
				"--upstream=http://127.0.0.1:9090/",
				"--logtostderr=true",
				"--v=10",
				"--tls-cert-file=/etc/tls-secret/tls.crt",
				"--tls-private-key-file=/etc/tls-secret/tls.key",
				"--client-ca-file=/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt",
				"--config-file=/etc/kube-rbac-config/config-file.json",
			},
			Ports: []corev1.ContainerPort{{
				Name:          "https",
				ContainerPort: int32(KubeRBACProxyPortNumber),
			}},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "serving-cert",
					MountPath: "/etc/tls-secret",
				},
				{
					Name:      "kube-rbac-config",
					MountPath: "/etc/kube-rbac-config",
				},
			},
		}},
		Volumes: []corev1.Volume{
			{
				Name: "serving-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: PrometheusServingCertSecretName,
					},
				},
			},
			{
				Name: "kube-rbac-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: PrometheusKubeRBACPoxyConfigMapName,
						},
					},
				},
			},
		},
	},
}
