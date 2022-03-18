/*
Copyright 2022.

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

package controllers

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promv1a1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/red-hat-storage/mcg-osd-deployer/templates"
	"github.com/red-hat-storage/mcg-osd-deployer/utils"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	alertRelabelConfigSecretKey            = "alertrelabelconfig.yaml"
	prometheusName                         = "managed-mcg-prometheus"
	monLabelKey                            = "app"
	monLabelValue                          = "managed-mcg"
	alertRelabelConfigSecretName           = "managed-mcg-alert-relabel-config-secret"
	alertmanagerName                       = "managed-mcg-alertmanager"
	notificationEmailKeyPrefix             = "notification-email"
	grafanaDatasourceSecretName            = "grafana-datasources-v2"
	grafanaDatasourceSecretKey             = "prometheus.yaml"
	k8sMetricsServiceMonitorAuthSecretName = "k8s-metrics-service-monitor-auth"
	openshiftMonitoringNamespace           = "openshift-monitoring"
	dmsRuleName                            = "dms-monitor-rule"
	alertmanagerConfigName                 = "managed-mcg-alertmanager-config"
	k8sMetricsServiceMonitorName           = "k8s-metrics-service-monitor"
	RouteName                              = "prometheus-route"
)

func (r *ManagedMCGReconciler) reconcileAlertMonitoring() error {
	if err := r.reconcileAlertRelabelConfigSecret(); err != nil {
		return err
	}
	if err := r.reconcilePrometheus(); err != nil {
		return err
	}
	if err := r.reconcileAlertmanager(); err != nil {
		return err
	}
	if err := r.reconcileAlertmanagerConfig(); err != nil {
		return err
	}
	if err := r.reconcileK8SMetricsServiceMonitorAuthSecret(); err != nil {
		return err
	}
	if err := r.reconcileK8SMetricsServiceMonitor(); err != nil {
		return err
	}
	if err := r.reconcileMonitoringResources(); err != nil {
		return err
	}
	if err := r.reconcileDMSPrometheusRule(); err != nil {
		return err
	}
	if err := r.reconcilePrometheusRoutes(); err != nil {
		return err
	}
	return nil
}

func (r *ManagedMCGReconciler) reconcileDMSPrometheusRule() error {
	r.Log.Info("Reconciling DMS Prometheus Rule")

	if err := r.get(r.dmsRule); err == nil {
		if err != nil {
			r.Log.Info("DMSPrometheusRule instnce already exists.")
			return nil

		}
	}
	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.dmsRule, func() error {
		if err := r.own(r.dmsRule); err != nil {
			return err
		}

		desired := templates.DMSPrometheusRuleTemplate.DeepCopy()

		for _, group := range desired.Spec.Groups {
			if group.Name == "snitch-alert" {
				for _, rule := range group.Rules {
					if rule.Alert == "DeadMansSnitch" {
						rule.Labels["namespace"] = r.namespace
					}
				}
			}
		}

		r.dmsRule.Spec = desired.Spec

		return nil
	})

	return err
}

func (r *ManagedMCGReconciler) reconcilePrometheusRoutes() error {
	r.Log.Info("Reconciling Prometheus Routes")
	if err := r.get(r.Route); err == nil {
		if err != nil {
			r.Log.Info("Route instnce already exists.")
			return nil

		}
	}
	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.Route, func() error {
		desired := templates.RouteTemplate.DeepCopy()
		r.Route.Spec = desired.Spec
		return nil
	})
	return err
}

func (r *ManagedMCGReconciler) reconcileK8SMetricsServiceMonitorAuthSecret() error {
	r.Log.Info("Reconciling k8sMetricsServiceMonitorAuthSecret")

	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.k8sMetricsServiceMonitorAuthSecret, func() error {
		if err := r.own(r.k8sMetricsServiceMonitorAuthSecret); err != nil {
			return err
		}

		secret := &corev1.Secret{}
		secret.Name = grafanaDatasourceSecretName
		secret.Namespace = openshiftMonitoringNamespace
		if err := r.unrestrictedGet(secret); err != nil {
			return fmt.Errorf("Failed to get grafana-datasources secret from openshift-monitoring namespace: %v", err)
		}

		authInfoStructure := struct {
			DataSources []struct {
				SecureJSONData struct {
					BasicAuthPassword string `json:"basicAuthPassword"`
				} `json:"secureJsonData"`
				BasicAuthUser string `json:"basicAuthUser"`
			}
		}{}

		if err := json.Unmarshal(secret.Data[grafanaDatasourceSecretKey], &authInfoStructure); err != nil {
			return fmt.Errorf("Could not unmarshal Grapana datasource data: %v", err)
		}

		r.k8sMetricsServiceMonitorAuthSecret.Data = nil
		for key := range authInfoStructure.DataSources {
			ds := &authInfoStructure.DataSources[key]
			if ds.BasicAuthUser == "internal" && ds.SecureJSONData.BasicAuthPassword != "" {
				r.k8sMetricsServiceMonitorAuthSecret.Data = map[string][]byte{
					"Username": []byte(ds.BasicAuthUser),
					"Password": []byte(ds.SecureJSONData.BasicAuthPassword),
				}
			}
		}
		if r.k8sMetricsServiceMonitorAuthSecret.Data == nil {
			return fmt.Errorf("Grapana datasource does not contain the needed credentials")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to update k8sMetricsServiceMonitorAuthSecret: %v", err)
	}
	return nil
}

func (r *ManagedMCGReconciler) reconcileK8SMetricsServiceMonitor() error {
	r.Log.Info("Reconciling k8sMetricsServiceMonitor")

	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.k8sMetricsServiceMonitor, func() error {
		if err := r.own(r.k8sMetricsServiceMonitor); err != nil {
			return err
		}
		desired := templates.K8sMetricsServiceMonitorTemplate.DeepCopy()
		r.k8sMetricsServiceMonitor.Spec = desired.Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to update k8sMetricsServiceMonitor: %v", err)
	}
	return nil
}

// reconcileMonitoringResources labels all monitoring resources (ServiceMonitors, PodMonitors, and PrometheusRules)
// found in the target namespace with a label that matches the label selector the defined on the Prometheus resource
// we are reconciling in reconcilePrometheus. Doing so instructs the Prometheus instance to notice and react to these labeled
// monitoring resources
func (r *ManagedMCGReconciler) reconcileMonitoringResources() error {
	r.Log.Info("reconciling monitoring resources")

	podMonitorList := promv1.PodMonitorList{}
	if err := r.list(&podMonitorList); err != nil {
		return fmt.Errorf("Could not list pod monitors: %v", err)
	}
	for i := range podMonitorList.Items {
		obj := podMonitorList.Items[i]
		utils.AddLabel(obj, monLabelKey, monLabelValue)
		if err := r.update(obj); err != nil {
			return err
		}
	}

	serviceMonitorList := promv1.ServiceMonitorList{}
	if err := r.list(&serviceMonitorList); err != nil {
		return fmt.Errorf("Could not list service monitors: %v", err)
	}
	for i := range serviceMonitorList.Items {
		obj := serviceMonitorList.Items[i]
		utils.AddLabel(obj, monLabelKey, monLabelValue)
		if err := r.update(obj); err != nil {
			return err
		}
	}

	promRuleList := promv1.PrometheusRuleList{}
	if err := r.list(&promRuleList); err != nil {
		return fmt.Errorf("Could not list prometheus rules: %v", err)
	}
	for i := range promRuleList.Items {
		obj := promRuleList.Items[i]
		utils.AddLabel(obj, monLabelKey, monLabelValue)
		if err := r.update(obj); err != nil {
			return err
		}
	}

	return nil
}

func (r *ManagedMCGReconciler) reconcileAlertmanagerConfig() error {
	r.Log.Info("Reconciling AlertmanagerConfig secret")

	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.alertmanagerConfig, func() error {
		if err := r.own(r.alertmanagerConfig); err != nil {
			return err
		}

		if err := r.get(r.pagerdutySecret); err != nil {
			return fmt.Errorf("Unable to get pagerduty secret: %v", err)
		}
		pagerdutySecretData := r.pagerdutySecret.Data
		pagerdutyServiceKey := string(pagerdutySecretData["PAGERDUTY_KEY"])
		if pagerdutyServiceKey == "" {
			return fmt.Errorf("Pagerduty secret does not contain a PAGERDUTY_KEY entry")
		}

		if r.deadMansSnitchSecret.UID == "" {
			if err := r.get(r.deadMansSnitchSecret); err != nil {
				return fmt.Errorf("Unable to get DeadMan's Snitch secret: %v", err)
			}
		}
		dmsURL := string(r.deadMansSnitchSecret.Data["SNITCH_URL"])
		if dmsURL == "" {
			return fmt.Errorf("DeadMan's Snitch secret does not contain a SNITCH_URL entry")
		}

		alertingAddressList := []string{}
		i := 0
		for {
			alertingAddressKey := fmt.Sprintf("%s-%v", notificationEmailKeyPrefix, i)
			alertingAddress, found := r.addonParams[alertingAddressKey]
			i++
			if found {
				alertingAddressAsString := alertingAddress
				if alertingAddressAsString != "" {
					alertingAddressList = append(alertingAddressList, alertingAddressAsString)
				}
			} else {
				break
			}
		}

		smtpSecretData := map[string][]byte{}
		if r.smtpSecret.UID == "" {
			if err := r.get(r.smtpSecret); err != nil {
				return fmt.Errorf("Unable to get SMTP secret : %v", err)
			}
		}
		smtpSecretData = r.smtpSecret.Data
		smtpHost := string(smtpSecretData["host"])
		if smtpHost == "" {
			return fmt.Errorf("smtp secret does not contain a host entry")
		}
		smtpPort := string(smtpSecretData["port"])
		if smtpPort == "" {
			return fmt.Errorf("smtp secret does not contain a port entry")
		}
		smtpUsername := string(smtpSecretData["username"])
		if smtpUsername == "" {
			return fmt.Errorf("smtp secret does not contain a username entry")
		}
		smtpPassword := string(smtpSecretData["password"])
		if smtpPassword == "" {
			return fmt.Errorf("smtp secret does not contain a password entry.")
		}
		smtpHTML, err := ioutil.ReadFile(r.CustomerNotificationHTMLPath)
		if err != nil {
			return fmt.Errorf("unable to read customernotification.html file: %v", err)
		}

		desired := templates.AlertmanagerConfigTemplate.DeepCopy()
		for i := range desired.Spec.Receivers {
			receiver := &desired.Spec.Receivers[i]
			switch receiver.Name {
			case "pagerduty":
				receiver.PagerDutyConfigs[0].ServiceKey.Key = "PAGERDUTY_KEY"
				receiver.PagerDutyConfigs[0].ServiceKey.LocalObjectReference.Name = r.PagerdutySecretName
				receiver.PagerDutyConfigs[0].Details[0].Key = "SOP"
				receiver.PagerDutyConfigs[0].Details[0].Value = r.SOPEndpoint
			case "DeadMansSnitch":
				receiver.WebhookConfigs[0].URL = &dmsURL
			case "SendGrid":
				if len(alertingAddressList) > 0 {
					receiver.EmailConfigs[0].Smarthost = fmt.Sprintf("%s:%s", smtpHost, smtpPort)
					receiver.EmailConfigs[0].AuthUsername = smtpUsername
					receiver.EmailConfigs[0].AuthPassword.LocalObjectReference.Name = r.SMTPSecretName
					receiver.EmailConfigs[0].AuthPassword.Key = "password"
					receiver.EmailConfigs[0].From = r.AlertSMTPFrom
					receiver.EmailConfigs[0].To = strings.Join(alertingAddressList, ", ")
					receiver.EmailConfigs[0].HTML = string(smtpHTML)
				} else {
					r.Log.V(-1).Info("Customer Email for alert notification is not provided")
					receiver.EmailConfigs = []promv1a1.EmailConfig{}
				}
			}

		}
		r.alertmanagerConfig.Spec = desired.Spec
		utils.AddLabel(r.alertmanagerConfig, monLabelKey, monLabelValue)

		return nil
	})

	return err
}

func (r *ManagedMCGReconciler) reconcileAlertmanager() error {
	r.Log.Info("Reconciling Alertmanager")
	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.alertmanager, func() error {
		if err := r.own(r.alertmanager); err != nil {
			return err
		}

		desired := templates.AlertmanagerTemplate.DeepCopy()
		desired.Spec.AlertmanagerConfigSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				monLabelKey: monLabelValue,
			},
		}
		r.alertmanager.Spec = desired.Spec
		utils.AddLabel(r.alertmanager, monLabelKey, monLabelValue)

		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *ManagedMCGReconciler) reconcilePrometheus() error {
	r.Log.Info("Reconciling Prometheus")
	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.prometheus, func() error {
		if err := r.own(r.prometheus); err != nil {
			return err
		}
		desired := templates.PrometheusTemplate.DeepCopy()
		r.prometheus.ObjectMeta.Labels = map[string]string{monLabelKey: monLabelValue}
		r.prometheus.Spec = desired.Spec
		r.prometheus.Spec.Alerting.Alertmanagers[0].Namespace = r.namespace
		r.prometheus.Spec.AdditionalAlertRelabelConfigs = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: alertRelabelConfigSecretName,
			},
			Key: alertRelabelConfigSecretKey,
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// AlertRelabelConfigSecret will have configuration for relabeling the alerts that are firing.
// It will add namespace label to firing alerts before they are sent to the alertmanager
func (r *ManagedMCGReconciler) reconcileAlertRelabelConfigSecret() error {
	r.Log.Info("Reconciling alertRelabelConfigSecret")

	_, err := ctrl.CreateOrUpdate(r.ctx, r.Client, r.alertRelabelConfigSecret, func() error {
		if err := r.own(r.alertRelabelConfigSecret); err != nil {
			return err
		}

		alertRelabelConfig := []struct {
			TargetLabel string `yaml:"target_label,omitempty"`
			Replacement string `yaml:"replacement,omitempty"`
		}{{
			TargetLabel: "namespace",
			Replacement: r.namespace,
		}}

		config, err := yaml.Marshal(alertRelabelConfig)
		if err != nil {
			return fmt.Errorf("Unable to encode alert relabel conifg: %v", err)
		}
		r.alertRelabelConfigSecret.Data = map[string][]byte{
			alertRelabelConfigSecretKey: config,
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("Unable to create/update AlertRelabelConfigSecret: %v", err)
	}

	return nil
}
