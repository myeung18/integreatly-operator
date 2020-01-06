package fuseonopenshift

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/integr8ly/integreatly-operator/pkg/apis/integreatly/v1alpha1"
	"github.com/integr8ly/integreatly-operator/pkg/controller/installation/marketplace"
	"github.com/integr8ly/integreatly-operator/pkg/controller/installation/products/config"
	"github.com/integr8ly/integreatly-operator/pkg/resources"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	samplesv1 "github.com/openshift/cluster-samples-operator/pkg/apis/samples/v1"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pkgclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	fuseOnOpenshiftNs      = "openshift"
	TemplatesBaseURL       = "https://raw.githubusercontent.com/jboss-fuse/application-templates/"
	templatesConfigMapName = "fuse-on-openshift-templates"
	imageStreamFileName    = "fis-image-streams.json"
)

var (
	quickstartTemplates = []string{
		"eap-camel-amq-template.json",
		"eap-camel-cdi-template.json",
		"eap-camel-cxf-jaxrs-template.json",
		"eap-camel-cxf-jaxws-template.json",
		"eap-camel-jpa-template.json",
		"karaf-camel-amq-template.json",
		"karaf-camel-log-template.json",
		"karaf-camel-rest-sql-template.json",
		"karaf-cxf-rest-template.json",
		"spring-boot-camel-amq-template.json",
		"spring-boot-camel-config-template.json",
		"spring-boot-camel-drools-template.json",
		"spring-boot-camel-infinispan-template.json",
		"spring-boot-camel-rest-3scale-template.json",
		"spring-boot-camel-rest-sql-template.json",
		"spring-boot-camel-teiid-template.json",
		"spring-boot-camel-template.json",
		"spring-boot-camel-xa-template.json",
		"spring-boot-camel-xml-template.json",
		"spring-boot-cxf-jaxrs-template.json",
		"spring-boot-cxf-jaxws-template.json",
	}
	consoleTemplates = []string{
		"fuse-console-cluster-os4.json",
		"fuse-console-namespace-os4.json",
		"fuse-apicurito.yml",
	}
)

type Reconciler struct {
	*resources.Reconciler
	coreClient    kubernetes.Interface
	Config        *config.FuseOnOpenshift
	ConfigManager config.ConfigReadWriter
	httpClient    http.Client
	logger        *logrus.Entry
}

func (r *Reconciler) GetPreflightObject(ns string) runtime.Object {
	return nil
}

func NewReconciler(configManager config.ConfigReadWriter, instance *v1alpha1.Installation, mpm marketplace.MarketplaceInterface) (*Reconciler, error) {
	config, err := configManager.ReadFuseOnOpenshift()
	if err != nil {
		return nil, errors.Wrapf(err, "could not retrieve %s config", v1alpha1.ProductFuseOnOpenshift)
	}

	if config.GetNamespace() == "" {
		config.SetNamespace(fuseOnOpenshiftNs)
	}

	if err = config.Validate(); err != nil {
		return nil, errors.Wrapf(err, "%s config is not valid", v1alpha1.ProductFuseOnOpenshift)
	}

	logger := logrus.NewEntry(logrus.StandardLogger())
	var httpClient http.Client

	return &Reconciler{
		ConfigManager: configManager,
		Config:        config,
		logger:        logger,
		httpClient:    httpClient,
		Reconciler:    resources.NewReconciler(mpm),
	}, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, inst *v1alpha1.Installation, product *v1alpha1.InstallationProductStatus, serverClient pkgclient.Client) (v1alpha1.StatusPhase, error) {
	phase, err := r.reconcileConfigMap(ctx, serverClient)
	if err != nil || phase != v1alpha1.PhaseCompleted {
		return phase, err
	}

	phase, err = r.reconcileImageStreams(ctx, serverClient, inst)
	if err != nil || phase != v1alpha1.PhaseCompleted {
		return phase, err
	}

	phase, err = r.reconcileTemplates(ctx, serverClient, inst)
	if err != nil || phase != v1alpha1.PhaseCompleted {
		return phase, err
	}

	product.Version = r.Config.GetProductVersion()
	product.OperatorVersion = r.Config.GetOperatorVersion()

	logrus.Infof("[%s] successfully reconciled", v1alpha1.ProductFuseOnOpenshift)
	return v1alpha1.PhaseCompleted, nil
}

func (r *Reconciler) reconcileConfigMap(ctx context.Context, serverClient pkgclient.Client) (v1alpha1.StatusPhase, error) {
	logrus.Infoln("Reconciling Fuse on OpenShift templates config map")
	cfgMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templatesConfigMapName,
			Namespace: r.ConfigManager.GetOperatorNamespace(),
		},
	}

	cfgMap, err := r.getTemplatesConfigMap(ctx, serverClient)
	if err != nil && !k8errors.IsNotFound(err) {
		return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to get config map %s from %s namespace", cfgMap.Name, cfgMap.Namespace)
	}

	// Create configmap if not found
	if k8errors.IsNotFound(err) {
		configMapData := make(map[string]string)
		fileNames := []string{
			imageStreamFileName,
		}
		fileNames = append(fileNames, consoleTemplates...)

		for _, qn := range quickstartTemplates {
			fileNames = append(fileNames, "quickstarts/"+qn)
		}

		for _, fn := range fileNames {
			fileURL := TemplatesBaseURL + string(r.Config.GetProductVersion()) + "/" + fn
			content, err := r.getFileContentFromURL(fileURL)
			if err != nil {
				return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to get file contents of %s", fn)
			}
			defer content.Close()

			data, err := ioutil.ReadAll(content)
			if err != nil {
				return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to read contents of %s", fn)
			}

			// Removes 'quickstarts/' from the key prefix as this is not a valid configmap data key
			key := strings.TrimPrefix(fn, "quickstarts/")

			// Write content of file to configmap
			configMapData[key] = string(data)
		}

		cfgMap.Data = configMapData
		if err := serverClient.Create(ctx, cfgMap); err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to create configmap %s in %s namespace", cfgMap.Name, cfgMap.Namespace)
		}
	}

	return v1alpha1.PhaseCompleted, nil
}

func (r *Reconciler) reconcileImageStreams(ctx context.Context, serverClient pkgclient.Client, inst *v1alpha1.Installation) (v1alpha1.StatusPhase, error) {
	logrus.Infoln("Reconciling Fuse on OpenShift imagestreams")
	cfgMap, err := r.getTemplatesConfigMap(ctx, serverClient)
	if err != nil {
		return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to get configmap %s from %s namespace", cfgMap.Name, cfgMap.Data)
	}

	content := []byte(cfgMap.Data[imageStreamFileName])

	var fileContent map[string]interface{}
	if err := json.Unmarshal(content, &fileContent); err != nil {
		return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to unmarshal contents of %s", imageStreamFileName)
	}

	// The content of the imagestream file is a an object of kind List
	// Create the imagestreams seperately
	isList := r.getResourcesFromList(fileContent)
	imageStreams := make(map[string]runtime.Object)
	for _, is := range isList {
		jsonData, err := json.Marshal(is)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to marshal data %s", imageStreamFileName)
		}

		imageStreamRuntimeObj, err := resources.LoadKubernetesResource(jsonData, r.Config.GetNamespace(), inst)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrap(err, "failed to load kubernetes imagestream resource")
		}

		// Get unstructured of image stream so we can retrieve the image stream name
		imageStreamUnstructured, err := resources.UnstructuredFromRuntimeObject(imageStreamRuntimeObj)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrap(err, "failed to parse runtime object to unstructured for imagestream")
		}

		imageStreamName := imageStreamUnstructured.GetName()
		imageStreams[imageStreamName] = imageStreamRuntimeObj
	}

	imageStreamNames := r.getKeysFromMap(imageStreams)

	// Update the sample cluster sample operator CR to skip the Fuse on OpenShift image streams
	if err := r.updateClusterSampleCR(ctx, serverClient, "SkippedImagestreams", imageStreamNames); err != nil {
		return v1alpha1.PhaseFailed, errors.Wrap(err, "failed to update SkippedImagestreams in cluster sample custom resource")
	}

	for isName, isObj := range imageStreams {
		if err := r.createResourceIfNotExist(ctx, serverClient, isObj); err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to create image stream %s", isName)
		}
	}

	return v1alpha1.PhaseCompleted, nil
}

func (r *Reconciler) reconcileTemplates(ctx context.Context, serverClient pkgclient.Client, inst *v1alpha1.Installation) (v1alpha1.StatusPhase, error) {
	logrus.Infoln("Reconciling Fuse on OpenShift templates")
	var templateFiles []string
	templates := make(map[string]runtime.Object)

	templateFiles = append(templateFiles, consoleTemplates...)
	templateFiles = append(templateFiles, quickstartTemplates...)

	for _, fileName := range templateFiles {
		cfgMap, err := r.getTemplatesConfigMap(ctx, serverClient)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to get configmap %s from %s namespace", cfgMap.Name, cfgMap.Data)
		}

		content := []byte(cfgMap.Data[fileName])

		if filepath.Ext(fileName) == ".yml" || filepath.Ext(fileName) == ".yaml" {
			content, err = yaml.ToJSON(content)
			if err != nil {
				return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to convert yaml to json %s", fileName)
			}
		}

		templateRuntimeObj, err := resources.LoadKubernetesResource(content, r.Config.GetNamespace(), inst)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to load resource %s", fileName)
		}

		templateUnstructured, err := resources.UnstructuredFromRuntimeObject(templateRuntimeObj)
		if err != nil {
			return v1alpha1.PhaseFailed, errors.Wrap(err, "failed to parse object")
		}

		templateName := templateUnstructured.GetName()
		templates[templateName] = templateRuntimeObj
	}

	templateNames := r.getKeysFromMap(templates)

	// Update sample cluster operator CR to skip Fuse on OpenShift quickstart templates
	if err := r.updateClusterSampleCR(ctx, serverClient, "SkippedTemplates", templateNames); err != nil {
		return v1alpha1.PhaseFailed, errors.Wrap(err, "failed to update SkippedTemplates in cluster sample custom resource")
	}

	for name, obj := range templates {
		if err := r.createResourceIfNotExist(ctx, serverClient, obj); err != nil {
			return v1alpha1.PhaseFailed, errors.Wrapf(err, "failed to create image stream %s", name)
		}
	}

	return v1alpha1.PhaseCompleted, nil
}

func (r *Reconciler) getTemplatesConfigMap(ctx context.Context, serverClient pkgclient.Client) (*corev1.ConfigMap, error) {
	cfgMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templatesConfigMapName,
			Namespace: r.ConfigManager.GetOperatorNamespace(),
		},
	}

	err := serverClient.Get(ctx, pkgclient.ObjectKey{Name: cfgMap.Name, Namespace: cfgMap.Namespace}, cfgMap)
	return cfgMap, err
}

func (r *Reconciler) createResourceIfNotExist(ctx context.Context, serverClient pkgclient.Client, resource runtime.Object) error {
	u, err := resources.UnstructuredFromRuntimeObject(resource)
	if err != nil {
		return errors.Errorf("failed to get unstructured object of type %T from resource %s", resource, resource)
	}

	if err := serverClient.Get(ctx, pkgclient.ObjectKey{Name: u.GetName(), Namespace: u.GetNamespace()}, u); err != nil {
		if !k8errors.IsNotFound(err) {
			return errors.Wrap(err, "failed to get resource")
		}
		if err := serverClient.Create(ctx, resource); err != nil {
			return errors.Wrap(err, "failed to create resource")
		}
		return nil
	}

	if !r.resourceHasLabel(u.GetLabels(), "integreatly", "true") {
		if err := serverClient.Delete(ctx, resource); err != nil {
			return errors.Wrap(err, "failed to delete resource")
		}
		if err := serverClient.Create(ctx, resource); err != nil {
			return errors.Wrap(err, "failed to create resource")
		}
	}

	return nil
}

func (r *Reconciler) getFileContentFromURL(url string) (io.ReadCloser, error) {
	resp, err := r.httpClient.Get(url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	return nil, errors.Errorf("failed to get file content from %s. Status: %d", url, resp.StatusCode)
}

func (r *Reconciler) getResourcesFromList(listObj map[string]interface{}) []interface{} {
	items := reflect.ValueOf(listObj["items"])

	var resources []interface{}

	for i := 0; i < items.Len(); i++ {
		resources = append(resources, items.Index(i).Interface())
	}

	return resources
}

func (r *Reconciler) updateClusterSampleCR(ctx context.Context, serverClient pkgclient.Client, key string, value []string) error {
	clusterSampleCR := &samplesv1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}

	if err := serverClient.Get(ctx, pkgclient.ObjectKey{Name: clusterSampleCR.Name}, clusterSampleCR); err != nil {
		// If cluster sample cr is not found, the cluster sample operator is not installed so no need to update it
		if k8errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if key == "SkippedImagestreams" {
		for _, v := range value {
			if !r.contains(clusterSampleCR.Spec.SkippedImagestreams, v) {
				clusterSampleCR.Spec.SkippedImagestreams = append(clusterSampleCR.Spec.SkippedImagestreams, v)
			}
		}
	}

	if key == "SkippedTemplates" {
		for _, v := range value {
			if !r.contains(clusterSampleCR.Spec.SkippedTemplates, v) {
				clusterSampleCR.Spec.SkippedTemplates = append(clusterSampleCR.Spec.SkippedTemplates, v)
			}
		}
	}

	if err := serverClient.Update(ctx, clusterSampleCR); err != nil {
		return err
	}

	return nil
}

func (r *Reconciler) getKeysFromMap(mapObj map[string]runtime.Object) []string {
	var keys []string

	for k, _ := range mapObj {
		keys = append(keys, k)
	}
	return keys
}

func (r *Reconciler) resourceHasLabel(labels map[string]string, key, value string) bool {
	if val, ok := labels[key]; ok && val == value {
		return true
	}
	return false
}

func (r *Reconciler) contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}

	return false
}
