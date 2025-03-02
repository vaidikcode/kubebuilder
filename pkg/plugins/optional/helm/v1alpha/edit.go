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

package v1alpha

import (
	"fmt"

	"github.com/spf13/pflag"
	"sigs.k8s.io/kubebuilder/v4/pkg/config"
	"sigs.k8s.io/kubebuilder/v4/pkg/machinery"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugin"
	"sigs.k8s.io/kubebuilder/v4/pkg/plugins/optional/helm/v1alpha/scaffolds"
)

var _ plugin.EditSubcommand = &editSubcommand{}

type editSubcommand struct {
	config   config.Config
	force    bool
	chartDir string
}

//nolint:lll
func (p *editSubcommand) UpdateMetadata(cliMeta plugin.CLIMetadata, subcmdMeta *plugin.SubcommandMetadata) {
	subcmdMeta.Description = `Initialize or update a Helm chart to distribute the project under the dist/ directory.

**NOTE** Before running the edit command, ensure you first execute 'make manifests' to regenerate
the latest Helm chart with your most recent changes.`

	subcmdMeta.Examples = fmt.Sprintf(`# Initialize or update a Helm chart to distribute the project under the dist/ directory
  %[1]s edit --plugins=%[2]s

# Update the Helm chart under the dist/ directory and overwrite all files
  %[1]s edit --plugins=%[2]s --force

**IMPORTANT**: If the "--force" flag is not used, the following files will not be updated to preserve your customizations:
dist/chart/
├── values.yaml
└── templates/
    └── manager/
        └── manager.yaml

The following files are never updated after their initial creation:
  - chart/Chart.yaml
  - chart/templates/_helpers.tpl
  - chart/.helmignore

All other files are updated without the usage of the '--force=true' flag
when the edit option is used to ensure that the
manifests in the chart align with the latest changes.
`, cliMeta.CommandName, plugin.KeyFor(Plugin{}))
}

// Update the BindFlags method to add the chart-dir flag
func (p *editSubcommand) BindFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&p.force, "force", false, "if true, regenerates all the files")
	fs.StringVar(&p.chartDir, "chart-dir", "dist", "Directory where the Helm chart will be scaffolded")
}

// Update the Scaffold method to retrieve the stored chart directory
func (p *editSubcommand) Scaffold(fs machinery.Filesystem) error {
	// Try to get chartDir from PROJECT file
	cfg := pluginConfig{}
	if err := p.config.DecodePluginConfig(pluginKey, &cfg); err == nil {
		// If a directory was stored and none specified on command line, use the stored one
		if cfg.ChartDir != "" && p.chartDir == "dist" {
			p.chartDir = cfg.ChartDir
		}
	}

	// Use default if still not specified
	if p.chartDir == "" {
		p.chartDir = "dist"
	}

	scaffolder := scaffolds.NewInitHelmScaffolder(p.config, p.force, p.chartDir)
	scaffolder.InjectFS(fs)
	err := scaffolder.Scaffold()
	if err != nil {
		return err
	}

	// Track or update the chart directory in the PROJECT file
	return insertPluginMetaToConfig(p.config, pluginConfig{ChartDir: p.chartDir})
}
