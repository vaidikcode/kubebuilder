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

var _ plugin.InitSubcommand = &initSubcommand{}

type initSubcommand struct {
	config   config.Config
	chartDir string
}

func (p *initSubcommand) UpdateMetadata(cliMeta plugin.CLIMetadata, subcmdMeta *plugin.SubcommandMetadata) {
	subcmdMeta.Description = `Initialize a helm chart to distribute the project
`
	subcmdMeta.Examples = fmt.Sprintf(`# Initialize a helm chart in the default location (dist/)
  %[1]s init --plugins=%[2]s

# Initialize a helm chart in a custom location
  %[1]s init --plugins=%[2]s --chart-dir=charts

**IMPORTANT** You must use %[1]s edit --plugins=%[2]s to update the chart when changes are made.
`, cliMeta.CommandName, plugin.KeyFor(Plugin{}))
}

func (p *initSubcommand) InjectConfig(c config.Config) error {
	p.config = c
	return nil
}

// Add the BindFlags method to accept the chart-dir flag
func (p *initSubcommand) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&p.chartDir, "chart-dir", "dist", "Directory where the Helm chart will be scaffolded")
}

// Update the Scaffold method to use the chart directory
func (p *initSubcommand) Scaffold(fs machinery.Filesystem) error {
	// Use default if not specified
	if p.chartDir == "" {
		p.chartDir = "dist"
	}

	scaffolder := scaffolds.NewInitHelmScaffolder(p.config, false, p.chartDir)
	scaffolder.InjectFS(fs)
	err := scaffolder.Scaffold()
	if err != nil {
		return err
	}

	// Track the chart directory in the PROJECT file
	return insertPluginMetaToConfig(p.config, pluginConfig{ChartDir: p.chartDir})
}
