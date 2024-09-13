package adm

import (
	"context"
	"fmt"
	"os"

	"github.com/kubesaw/ksctl/pkg/client"
	"github.com/kubesaw/ksctl/pkg/cmd/flags"
	"github.com/kubesaw/ksctl/pkg/configuration"
	clicontext "github.com/kubesaw/ksctl/pkg/context"
	"github.com/kubesaw/ksctl/pkg/ioutils"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	kubectlrollout "k8s.io/kubectl/pkg/cmd/rollout"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// NewRestartCmd() is a function to restart the whole operator, it relies on the target cluster and fetches the cluster config
// 1.  If the command is run for host operator, it restart the whole host operator.(it deletes olm based pods(host-operator pods),
// waits for the new deployment to come up, then uses rollout-restart command for non-olm based - registration-service)
// 2.  If the command is run for member operator, it restart the whole member operator.(it deletes olm based pods(member-operator pods),
// waits for the new deployment to come up, then uses rollout-restart command for non-olm based deployments - webhooks)
func NewRestartCmd() *cobra.Command {
	var targetCluster string
	command := &cobra.Command{
		Use:   "restart -t <cluster-name> <host|member-1|member-2>",
		Short: "Restarts a deployment",
		Long: `Restarts the deployment with the given name in the operator namespace. 
If no deployment name is provided, then it lists all existing deployments in the namespace.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			term := ioutils.NewTerminal(cmd.InOrStdin, cmd.OutOrStdout)
			ctx := clicontext.NewCommandContext(term, client.DefaultNewClient)
			return restart(ctx, targetCluster, args...)
		},
	}
	command.Flags().StringVarP(&targetCluster, "target-cluster", "t", "", "The target cluster")
	flags.MustMarkRequired(command, "target-cluster")
	return command
}

func restart(ctx *clicontext.CommandContext, clusterName string, operatorType ...string) error {
	cfg, err := configuration.LoadClusterConfig(ctx, clusterName)
	if err != nil {
		return err
	}
	cl, err := ctx.NewClient(cfg.Token, cfg.ServerAPI)

	if err != nil {
		return err
	}

	if len(operatorType) == 0 {
		return fmt.Errorf("please mention one of the following operator names to restart: host | member-1 | member-2")
	}

	if !ctx.AskForConfirmation(
		ioutils.WithMessagef("restart the '%s' operator in namespace '%s'", operatorType[0], cfg.OperatorNamespace)) {
		return nil
	}

	return restartDeployment(ctx, cl, cfg.OperatorNamespace)
}

func restartDeployment(ctx *clicontext.CommandContext, cl runtimeclient.Client, ns string) error {
	olmDeploymentList, nonOlmDeploymentlist, err := getExistingDeployments(cl, ns)
	if err != nil {
		return err
	}

	if olmDeploymentList == nil {
		return fmt.Errorf("OLM based deploymont not found in %s", ns)
	}
	for _, olmDeployment := range olmDeploymentList.Items {
		if err := deletePods(ctx, cl, olmDeployment, ns); err != nil {
			return err
		}
	}
	if nonOlmDeploymentlist == nil {
		return fmt.Errorf("non-OLM based deploymont not found in %s", ns)
	}
	for _, nonOlmDeployment := range nonOlmDeploymentlist.Items {
		if err := restartNonOlmDeployments(ns, nonOlmDeployment); err != nil {
			return err
		}
		//check the rollout status
		if err := checkRolloutStatus(ns); err != nil {
			return err
		}
	}
	return nil
}

func deletePods(ctx *clicontext.CommandContext, cl runtimeclient.Client, deployment appsv1.Deployment, ns string) error {
	//get pods by label selector from the deployment
	pods := corev1.PodList{}
	selector, _ := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err := cl.List(ctx, &pods, runtimeclient.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}

	//delete pods
	for _, pod := range pods.Items {
		if err := cl.Delete(ctx, &pod); err != nil {
			return err
		}
	}

	//check the rollout status
	if err := checkRolloutStatus(ns); err != nil {
		return err
	}
	return nil

}

func restartNonOlmDeployments(ns string, deployment appsv1.Deployment) error {
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	hFactory := cmdutil.NewFactory(cmdutil.NewMatchVersionFlags(kubeConfigFlags))
	ioStreams := genericclioptions.IOStreams{
		In:     nil, // Not to forward the Standard Input
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	o := kubectlrollout.NewRolloutRestartOptions(ioStreams)

	if err := o.Complete(hFactory, nil, []string{"deployments"}); err != nil {
		panic(err)
	}
	o.Namespace = ns
	o.Resources = []string{"deployment/" + deployment.Name}

	if err := o.Validate(); err != nil {
		panic(err)
	}
	return o.RunRestart()
}

func checkRolloutStatus(ns string) error {
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	Factory := cmdutil.NewFactory(cmdutil.NewMatchVersionFlags(kubeConfigFlags))
	ioStreams := genericclioptions.IOStreams{
		In:     nil, // Not to forward the Standard Input
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	cmd := kubectlrollout.NewRolloutStatusOptions(ioStreams)

	if err := cmd.Complete(Factory, []string{"deployment"}); err != nil {
		panic(err)
	}
	cmd.LabelSelector = "provider=codeready-toolchain"
	cmd.Namespace = ns
	if err := cmd.Validate(); err != nil {
		panic(err)
	}
	return cmd.Run()
}

func getExistingDeployments(cl runtimeclient.Client, ns string) (*appsv1.DeploymentList, *appsv1.DeploymentList, error) {

	olmDeployments := &appsv1.DeploymentList{}
	if err := cl.List(context.TODO(), olmDeployments,
		runtimeclient.InNamespace(ns),
		runtimeclient.MatchingLabels{"olm.owner.kind": "ClusterServiceVersion"}); err != nil {
		return nil, nil, err
	}

	nonOlmDeployments := &appsv1.DeploymentList{}
	if err := cl.List(context.TODO(), nonOlmDeployments,
		runtimeclient.InNamespace(ns),
		runtimeclient.MatchingLabels{"provider": "codeready-toolchain"}); err != nil {
		return nil, nil, err
	}

	return olmDeployments, nonOlmDeployments, nil
}
