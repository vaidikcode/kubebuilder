/*
Copyright 2024 The Kubernetes Authors.

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

package scaffolds

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"

	log "github.com/sirupsen/logrus"

	"sigs.k8s.io/kubebuilder/v4/pkg/config"
	"sigs.k8s.io/kubebuilder/v4/pkg/machinery"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugin"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins/golang/deploy-image/v1alpha1"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates"
	charttemplates "sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates"
	templatescertmanager "sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates/cert-manager"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates/manager"
	templatesmetrics "sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates/metrics"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates/prometheus"
	templateswebhooks "sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/chart-templates/webhook"
	github "sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds/internal/templates/github"
)

var _ plugins.Scaffolder = &initScaffolder{}

type initScaffolder struct {
	config config.Config

	fs machinery.Filesystem

	force bool

	chartDir string
}

// NewInitHelmScaffolder returns a new Scaffolder for HelmPlugin
func NewInitHelmScaffolder(config config.Config, force bool, chartDir string) plugins.Scaffolder {
	return &initScaffolder{
		config:   config,
		force:    force,
		chartDir: chartDir,
	}
}

// InjectFS implements cmdutil.Scaffolder
func (s *initScaffolder) InjectFS(fs machinery.Filesystem) {
	s.fs = fs
}

// Scaffold scaffolds the Helm chart with the necessary files.
func (s *initScaffolder) Scaffold() error {
	log.Println("Generating Helm Chart to distribute project")

	imagesEnvVars := s.getDeployImagesEnvVars()

	mutatingWebhooks, validatingWebhooks, err := s.extractWebhooksFromGeneratedFiles()
	if err != nil {
		return fmt.Errorf("failed to extract webhooks: %w", err)
	}

	scaffold := machinery.NewScaffold(s.fs,
		machinery.WithConfig(s.config),
	)

	hasWebhooks := len(mutatingWebhooks) > 0 || len(validatingWebhooks) > 0
	buildScaffold := []machinery.Builder{
		&github.HelmChartCI{ChartDir: s.chartDir},
		&templates.HelmChart{ChartDir: s.chartDir},
		&templates.HelmValues{
			HasWebhooks:  hasWebhooks,
			DeployImages: imagesEnvVars,
			Force:        s.force,
			ChartDir:     s.chartDir,
		},
		&templates.HelmIgnore{ChartDir: s.chartDir},
		&charttemplates.HelmHelpers{ChartDir: s.chartDir},
		&manager.Deployment{
			Force:        s.force,
			DeployImages: len(imagesEnvVars) > 0,
			HasWebhooks:  hasWebhooks,
			ChartDir:     s.chartDir,
		},
		&templatescertmanager.Certificate{ChartDir: s.chartDir},
		&templatesmetrics.Service{ChartDir: s.chartDir},
		&prometheus.Monitor{ChartDir: s.chartDir},
	}

	if len(mutatingWebhooks) > 0 || len(validatingWebhooks) > 0 {
		buildScaffold = append(buildScaffold,
			&templateswebhooks.Template{
				MutatingWebhooks:   mutatingWebhooks,
				ValidatingWebhooks: validatingWebhooks,
				ChartDir:           s.chartDir,
			},
			&templateswebhooks.Service{ChartDir: s.chartDir},
		)
	}

	if err := scaffold.Execute(buildScaffold...); err != nil {
		return fmt.Errorf("error scaffolding helm-chart manifests: %v", err)
	}

	// Copy relevant files from config/ to chartDir/chart/templates/
	err = s.copyConfigFiles()
	if err != nil {
		return fmt.Errorf("failed to copy manifests from config to %s/chart/templates/: %v", s.chartDir, err)
	}

	return nil
}

// getDeployImagesEnvVars will return the values to append the envvars for projects
// which has the APIs scaffolded with DeployImage plugin
func (s *initScaffolder) getDeployImagesEnvVars() map[string]string {
	deployImages := make(map[string]string)

	pluginConfig := struct {
		Resources []struct {
			Kind    string            `json:"kind"`
			Options map[string]string `json:"options"`
		} `json:"resources"`
	}{}

	err := s.config.DecodePluginConfig(plugin.KeyFor(v1alpha1.Plugin{}), &pluginConfig)
	if err == nil {
		for _, res := range pluginConfig.Resources {
			image, ok := res.Options["image"]
			if ok {
				deployImages[strings.ToUpper(res.Kind)] = image
			}
		}
	}
	return deployImages
}

// extractWebhooksFromGeneratedFiles parses the files generated by controller-gen under
// config/webhooks and created Mutating and Validating helper structures to
// generate the webhook manifest for the helm-chart
func (s *initScaffolder) extractWebhooksFromGeneratedFiles() (mutatingWebhooks []templateswebhooks.DataWebhook,
	validatingWebhooks []templateswebhooks.DataWebhook, err error) {
	manifestFile := "config/webhook/manifests.yaml"

	if _, err := os.Stat(manifestFile); os.IsNotExist(err) {
		log.Printf("webhook manifests were not found at %s", manifestFile)
		return nil, nil, nil
	}

	content, err := os.ReadFile(manifestFile)
	if err != nil {
		return nil, nil,
			fmt.Errorf("failed to read %s: %w", manifestFile, err)
	}

	docs := strings.Split(string(content), "---")
	for _, doc := range docs {
		var webhookConfig struct {
			Kind     string `yaml:"kind"`
			Webhooks []struct {
				Name         string `yaml:"name"`
				ClientConfig struct {
					Service struct {
						Name      string `yaml:"name"`
						Namespace string `yaml:"namespace"`
						Path      string `yaml:"path"`
					} `yaml:"service"`
				} `yaml:"clientConfig"`
				Rules                   []templateswebhooks.DataWebhookRule `yaml:"rules"`
				FailurePolicy           string                              `yaml:"failurePolicy"`
				SideEffects             string                              `yaml:"sideEffects"`
				AdmissionReviewVersions []string                            `yaml:"admissionReviewVersions"`
			} `yaml:"webhooks"`
		}

		if err := yaml.Unmarshal([]byte(doc), &webhookConfig); err != nil {
			log.Errorf("fail to unmarshalling webhook YAML: %v", err)
			continue
		}

		for _, w := range webhookConfig.Webhooks {
			for i := range w.Rules {
				if len(w.Rules[i].APIGroups) == 0 {
					w.Rules[i].APIGroups = []string{""}
				}
			}
			webhook := templateswebhooks.DataWebhook{
				Name:                    w.Name,
				ServiceName:             fmt.Sprintf("%s-webhook-service", s.config.GetProjectName()),
				Path:                    w.ClientConfig.Service.Path,
				FailurePolicy:           w.FailurePolicy,
				SideEffects:             w.SideEffects,
				AdmissionReviewVersions: w.AdmissionReviewVersions,
				Rules:                   w.Rules,
			}

			if webhookConfig.Kind == "MutatingWebhookConfiguration" {
				mutatingWebhooks = append(mutatingWebhooks, webhook)
			} else if webhookConfig.Kind == "ValidatingWebhookConfiguration" {
				validatingWebhooks = append(validatingWebhooks, webhook)
			}
		}
	}

	return mutatingWebhooks, validatingWebhooks, nil
}

// Helper function to copy files from config/ to chartDir/chart/templates/
func (s *initScaffolder) copyConfigFiles() error {
	configDirs := []struct {
		SrcDir  string
		DestDir string
		SubDir  string
	}{
		{"config/rbac", filepath.Join(s.chartDir, "chart/templates/rbac"), "rbac"},
		{"config/crd/bases", filepath.Join(s.chartDir, "chart/templates/crd"), "crd"},
		{"config/network-policy", filepath.Join(s.chartDir, "chart/templates/network-policy"), "networkPolicy"},
	}

	for _, dir := range configDirs {
		// Check if the source directory exists
		if _, err := os.Stat(dir.SrcDir); os.IsNotExist(err) {
			// Skip if the source directory does not exist
			continue
		}

		files, err := filepath.Glob(filepath.Join(dir.SrcDir, "*.yaml"))
		if err != nil {
			return err
		}

		// Skip processing if the directory is empty (no matching files)
		if len(files) == 0 {
			continue
		}

		// Ensure destination directory exists
		if err := os.MkdirAll(dir.DestDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create directory %s: %v", dir.DestDir, err)
		}

		for _, srcFile := range files {
			destFile := filepath.Join(dir.DestDir, filepath.Base(srcFile))
			err := copyFileWithHelmLogic(srcFile, destFile, dir.SubDir, s.config.GetProjectName())
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFileWithHelmLogic reads the source file, modifies the content for Helm, applies patches
// to spec.conversion if applicable, and writes it to the destination
func copyFileWithHelmLogic(srcFile, destFile, subDir, projectName string) error {
	if _, err := os.Stat(srcFile); os.IsNotExist(err) {
		log.Printf("Source file does not exist: %s", srcFile)
		return err
	}

	content, err := os.ReadFile(srcFile)
	if err != nil {
		log.Printf("Error reading source file: %s", srcFile)
		return err
	}

	contentStr := string(content)

	// Skip kustomization.yaml or kustomizeconfig.yaml files
	if strings.HasSuffix(srcFile, "kustomization.yaml") ||
		strings.HasSuffix(srcFile, "kustomizeconfig.yaml") {
		return nil
	}

	// Apply RBAC-specific replacements
	if subDir == "rbac" {
		contentStr = strings.Replace(contentStr,
			"name: controller-manager",
			"name: {{ .Values.controllerManager.serviceAccountName }}", -1)
		contentStr = strings.Replace(contentStr,
			"name: metrics-reader",
			fmt.Sprintf("name: %s-metrics-reader", projectName), 1)

		contentStr = strings.Replace(contentStr,
			"name: metrics-auth-role",
			fmt.Sprintf("name: %s-metrics-auth-role", projectName), -1)
		contentStr = strings.Replace(contentStr,
			"name: metrics-auth-rolebinding",
			fmt.Sprintf("name: %s-metrics-auth-rolebinding", projectName), 1)

		if strings.Contains(contentStr, ".Values.controllerManager.serviceAccountName") &&
			strings.Contains(contentStr, "kind: ServiceAccount") &&
			!strings.Contains(contentStr, "RoleBinding") {
			// The generated Service Account does not have the annotations field so we must add it.
			contentStr = strings.Replace(contentStr,
				"metadata:", `metadata:
  {{- if and .Values.controllerManager.serviceAccount .Values.controllerManager.serviceAccount.annotations }}
  annotations:
    {{- range $key, $value := .Values.controllerManager.serviceAccount.annotations }}
    {{ $key }}: {{ $value }}
    {{- end }}
  {{- end }}`, 1)
		}
		contentStr = strings.Replace(contentStr,
			"name: leader-election-role",
			fmt.Sprintf("name: %s-leader-election-role", projectName), -1)
		contentStr = strings.Replace(contentStr,
			"name: leader-election-rolebinding",
			fmt.Sprintf("name: %s-leader-election-rolebinding", projectName), 1)
		contentStr = strings.Replace(contentStr,
			"name: manager-role",
			fmt.Sprintf("name: %s-manager-role", projectName), -1)
		contentStr = strings.Replace(contentStr,
			"name: manager-rolebinding",
			fmt.Sprintf("name: %s-manager-rolebinding", projectName), 1)

		// The generated files do not include the namespace
		if strings.Contains(contentStr, "leader-election-rolebinding") ||
			strings.Contains(contentStr, "leader-election-role") {
			namespace := `
  namespace: {{ .Release.Namespace }}`
			contentStr = strings.Replace(contentStr, "metadata:", "metadata:"+namespace, 1)
		}
	}

	// Conditionally handle CRD patches and annotations for CRDs
	if subDir == "crd" {
		kind, group := extractKindAndGroupFromFileName(filepath.Base(srcFile))
		hasWebhookPatch := false

		// Retrieve patch content for the CRD's spec.conversion, if it exists
		patchContent, patchExists, err := getCRDPatchContent(kind, group)
		if err != nil {
			return err
		}

		// If patch content exists, inject it under spec.conversion with Helm conditional
		if patchExists {
			conversionSpec := extractConversionSpec(patchContent)
			contentStr = injectConversionSpecWithCondition(contentStr, conversionSpec)
			hasWebhookPatch = true
		}

		// Inject annotations after "annotations:" in a single block without extra spaces
		contentStr = injectAnnotations(contentStr, hasWebhookPatch)
	}

	// Remove existing labels if necessary
	contentStr = removeLabels(contentStr)

	// Replace namespace with Helm template variable
	contentStr = strings.ReplaceAll(contentStr, "namespace: system", "namespace: {{ .Release.Namespace }}")

	contentStr = strings.Replace(contentStr, "metadata:", `metadata:
  labels:
    {{- include "chart.labels" . | nindent 4 }}`, 1)

	var wrappedContent string
	if isMetricRBACFile(subDir, srcFile) {
		wrappedContent = fmt.Sprintf(
			"{{- if and .Values.rbac.enable .Values.metrics.enable }}\n%s{{- end -}}\n", contentStr)
	} else {
		wrappedContent = fmt.Sprintf(
			"{{- if .Values.%s.enable }}\n%s{{- end -}}\n", subDir, contentStr)
	}

	if err := os.MkdirAll(filepath.Dir(destFile), os.ModePerm); err != nil {
		return err
	}

	err = os.WriteFile(destFile, []byte(wrappedContent), os.ModePerm)
	if err != nil {
		log.Printf("Error writing destination file: %s", destFile)
		return err
	}

	log.Printf("Successfully copied %s to %s", srcFile, destFile)
	return nil
}

// extractKindAndGroupFromFileName extracts the kind and group from a CRD filename
func extractKindAndGroupFromFileName(fileName string) (kind, group string) {
	parts := strings.Split(fileName, "_")
	if len(parts) >= 2 {
		group = strings.Split(parts[0], ".")[0] // Extract group up to the first dot
		kind = strings.TrimSuffix(parts[1], ".yaml")
	}
	return kind, group
}

// getCRDPatchContent finds and reads the appropriate patch content for a given kind and group
func getCRDPatchContent(kind, group string) (string, bool, error) {
	// First, look for patches that contain both "webhook", the group, and kind in their filename
	groupKindPattern := fmt.Sprintf("config/crd/patches/webhook_*%s*%s*.yaml", group, kind)
	patchFiles, err := filepath.Glob(groupKindPattern)
	if err != nil {
		return "", false, fmt.Errorf("failed to list patches: %v", err)
	}

	// If no group-specific patch found, search for patches that contain only "webhook" and the kind
	if len(patchFiles) == 0 {
		kindOnlyPattern := fmt.Sprintf("config/crd/patches/webhook_*%s*.yaml", kind)
		patchFiles, err = filepath.Glob(kindOnlyPattern)
		if err != nil {
			return "", false, fmt.Errorf("failed to list patches: %v", err)
		}
	}

	// Read the first matching patch file (if any)
	if len(patchFiles) > 0 {
		patchContent, err := os.ReadFile(patchFiles[0])
		if err != nil {
			return "", false, fmt.Errorf("failed to read patch file %s: %v", patchFiles[0], err)
		}
		return string(patchContent), true, nil
	}

	return "", false, nil
}

// extractConversionSpec extracts only the conversion section from the patch content
func extractConversionSpec(patchContent string) string {
	specStart := strings.Index(patchContent, "conversion:")
	if specStart == -1 {
		return ""
	}
	return patchContent[specStart:]
}

// injectConversionSpecWithCondition inserts the conversion spec under the main spec field with Helm conditional
func injectConversionSpecWithCondition(contentStr, conversionSpec string) string {
	specPosition := strings.Index(contentStr, "spec:")
	if specPosition == -1 {
		return contentStr // No spec field found; return unchanged
	}
	conditionalSpec := fmt.Sprintf("\n  {{- if .Values.webhook.enable }}\n  %s\n  {{- end }}",
		strings.TrimRight(conversionSpec, "\n"))
	return contentStr[:specPosition+5] + conditionalSpec + contentStr[specPosition+5:]
}

// injectAnnotations inserts the required annotations after the "annotations:" field in a single block without
// extra spaces
func injectAnnotations(contentStr string, hasWebhookPatch bool) string {
	annotationsBlock := `
    {{- if .Values.certmanager.enable }}
    cert-manager.io/inject-ca-from: "{{ .Release.Namespace }}/serving-cert"
    {{- end }}
    {{- if .Values.crd.keep }}
    "helm.sh/resource-policy": keep
    {{- end }}`
	if hasWebhookPatch {
		return strings.Replace(contentStr, "annotations:", "annotations:"+annotationsBlock, 1)
	}

	// Apply only resource policy if no webhook patch
	resourcePolicy := `
    {{- if .Values.crd.keep }}
    "helm.sh/resource-policy": keep
    {{- end }}`
	return strings.Replace(contentStr, "annotations:", "annotations:"+resourcePolicy, 1)
}

// isMetricRBACFile checks if the file is in the "rbac"
// subdirectory and matches one of the metric-related RBAC filenames
func isMetricRBACFile(subDir, srcFile string) bool {
	return subDir == "rbac" && (strings.HasSuffix(srcFile, "metrics_auth_role.yaml") ||
		strings.HasSuffix(srcFile, "metrics_auth_role_binding.yaml") ||
		strings.HasSuffix(srcFile, "metrics_reader_role.yaml"))
}

// removeLabels removes any existing labels section from the content
func removeLabels(content string) string {
	labelRegex := regexp.MustCompile(`(?m)^  labels:\n(?:    [^\n]+\n)*`)
	return labelRegex.ReplaceAllString(content, "")
}
