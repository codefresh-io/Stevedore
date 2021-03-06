package kubernetes

import (
	"fmt"

	"github.com/codefresh-io/stevedore/pkg/codefresh"
	"github.com/codefresh-io/stevedore/pkg/reporter"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeConfig "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

type (
	API interface {
		GoOverAllContexts()
		GoOverContextByName(string, string, string, bool, string)
		GoOverCurrentContext()
	}

	kubernetes struct {
		config    *api.Config
		codefresh codefresh.API
		reporter  reporter.Reporter
	}
)

func getDefaultOverride() clientcmd.ConfigOverrides {
	return clientcmd.ConfigOverrides{
		ClusterInfo: api.Cluster{
			Server: "",
		},
	}
}

type getOverContextOptions struct {
	contextName    string
	namespace      string
	serviceaccount string
	config         clientcmd.ClientConfig
	logger         *log.Entry
	codefresh      codefresh.API
	reporter       reporter.Reporter
	behindFirewall bool
	name           string
}

func goOverContext(options *getOverContextOptions) error {
	var host string
	var ca []byte
	var token []byte
	clientCnf, e := options.config.ClientConfig()
	if e != nil {
		message := fmt.Sprintf("Failed to create config with error:\n%s", e)
		options.logger.Warn(message)
		clientCnf, e = rest.InClusterConfig()
		if e != nil {
			message = fmt.Sprintf("Failed to create in cluster config with error:\n%s", e)
			options.logger.Warn(message)
			return e
		}
	}
	options.logger.Info("Created config for context")
	host = clientCnf.Host

	options.logger.Info("Creating rest client")
	clientset, e := kubeConfig.NewForConfig(clientCnf)
	if e != nil {
		message := fmt.Sprintf("Failed to create kubernetes client with error:\n%s", e)
		options.logger.Warn(message)
		
		return e
	}
	options.logger.Info("Created client set for context")

	options.logger.Info("Fetching service account from cluster")
	sa, e := clientset.CoreV1().ServiceAccounts(options.namespace).Get(options.serviceaccount, metav1.GetOptions{})
	if e != nil {
		message := fmt.Sprintf("Failed to get service account token with error:\n%s", e)
		options.logger.Warn(message)
		return e
	}
	if sa == nil {
		message := fmt.Sprintf("Service account: %s not found in namespace: %s", options.serviceaccount, options.namespace)
		options.logger.Warn(message)
		return fmt.Errorf(message)
	}
	if len(sa.Secrets) == 0 {
		message := fmt.Sprintf("Service account has no secrect configured for serviceaccount: %s", options.serviceaccount)
		options.logger.Warn(message)
		return fmt.Errorf(message)
	}
	secretName := string(sa.Secrets[0].Name)
	namespace := sa.Namespace
	options.logger.WithFields(log.Fields{
		"secret_name": secretName,
		"namespace":   namespace,
	}).Info(fmt.Sprint("Found service account accisiated with secret"))

	options.logger.Info("Fetching secret from cluster")
	secret, e := clientset.CoreV1().Secrets(namespace).Get(secretName, metav1.GetOptions{})
	if e != nil {
		message := fmt.Sprintf("Failed to get secrets with error:\n%s", e)
		options.logger.Warn(message)
		return e
	}
	token = secret.Data["token"]
	ca = secret.Data["ca.crt"]
	options.logger.Info(fmt.Sprint("Found secret"))

	options.logger.Info(fmt.Sprint("Creating cluster in Codefresh"))
	result, e := options.codefresh.Create(host, options.name, token, ca, options.behindFirewall)
	if e != nil {
		message := fmt.Sprintf("Failed to add cluster with error:\n%s", e)
		options.logger.Error(message)
		return e
	}
	options.reporter.AddToReport(options.contextName, reporter.SUCCESS, string(result))
	options.logger.Info(fmt.Sprint("Cluster added!"))
	return nil
}

func (kube *kubernetes) GoOverAllContexts() {
	contexts := kube.config.Contexts
	for contextName := range contexts {
		logger := log.WithFields(log.Fields{
			"context_name": contextName,
		})
		logger.Info("Working on context")
		logger.Info("Creating config")
		override := getDefaultOverride()
		config := clientcmd.NewNonInteractiveClientConfig(*kube.config, contextName, &override, nil)
		options := &getOverContextOptions{
			contextName:    contextName,
			config:         config,
			logger:         logger,
			codefresh:      kube.codefresh,
			reporter:       kube.reporter,
			behindFirewall: false,
			name:           contextName,
		}
		err := goOverContext(options)
		if err != nil {
			kube.reporter.AddToReport(contextName, reporter.FAILED, err.Error())
			continue
		}
	}
}

func (kube *kubernetes) GoOverContextByName(contextName string, namespace string, serviceaccount string, bf bool, name string) {
	var override clientcmd.ConfigOverrides
	var config clientcmd.ClientConfig
	override = getDefaultOverride()
	config = clientcmd.NewNonInteractiveClientConfig(*kube.config, contextName, &override, nil)
	logger := log.WithFields(log.Fields{
		"context_name":    contextName,
		"namespace":       namespace,
		"serviceaccount":  serviceaccount,
		"behind_firewall": bf,
		"name":            name,
	})
	options := &getOverContextOptions{
		contextName:    contextName,
		config:         config,
		logger:         logger,
		codefresh:      kube.codefresh,
		reporter:       kube.reporter,
		namespace:      namespace,
		serviceaccount: serviceaccount,
		behindFirewall: bf,
		name:           name,
	}
	err := goOverContext(options)
	if err != nil {
		kube.reporter.AddToReport(contextName, reporter.FAILED, err.Error())
	}
}

func (kube *kubernetes) GoOverCurrentContext() {
	override := getDefaultOverride()
	config := clientcmd.NewDefaultClientConfig(*kube.config, &override)
	rawConfig, err := config.RawConfig()
	if err != nil {
		kube.reporter.AddToReport("current-context", reporter.FAILED, err.Error())
	}
	contextName := rawConfig.CurrentContext
	logger := log.WithFields(log.Fields{
		"context_name": contextName,
	})
	options := &getOverContextOptions{
		contextName:    contextName,
		config:         config,
		logger:         logger,
		codefresh:      kube.codefresh,
		reporter:       kube.reporter,
		behindFirewall: false,
		name:           contextName,
	}
	err = goOverContext(options)
	if err != nil {
		kube.reporter.AddToReport(contextName, reporter.FAILED, err.Error())
	}
}

func NewKubernetesAPI(kubeConfigPath string, codefresh codefresh.API, reporter reporter.Reporter) API {
	return &kubernetes{
		config:    clientcmd.GetConfigFromFileOrDie(kubeConfigPath),
		codefresh: codefresh,
		reporter:  reporter,
	}
}
