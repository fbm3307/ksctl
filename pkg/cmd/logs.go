package cmd

import (
	"github.com/kubesaw/ksctl/pkg/kubectl"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	kubectllogs "k8s.io/kubectl/pkg/cmd/logs"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

func NewLogsCmd() *cobra.Command {
	return kubectl.SetUpKubectlCmd(func(factory cmdutil.Factory, ioStreams genericclioptions.IOStreams) *cobra.Command {
		return kubectllogs.NewCmdLogs(factory, ioStreams)
	})
}
