package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
    "time"

	"github.com/sirupsen/logrus"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func main() {
	setLogger()

	http.HandleFunc("/validate", ServerNamespaceBackup)
	http.HandleFunc("/health", ServerHealth)

	cert := "/etc/admission-webhook/tls/tls.crt"
	key := "/etc/admission-webhook/tls/tls.key"
	logrus.Print("Listening on port 443...")
	logrus.Fatal(http.ListenAndServeTLS(":443", cert, key, nil))
}

func ServerNamespaceBackup(w http.ResponseWriter, r *http.Request) {
	logger := logrus.WithFields(logrus.Fields{"uri": r.RequestURI})
	logger.Debug("Received mutation request")

	admissionReview, err := parseRequest(*r)
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse request")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	namespace := corev1.Namespace{}
    oldNamespace := corev1.Namespace{}
	err = json.Unmarshal(admissionReview.Request.Object.Raw, &namespace)
    if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse namespace")
		http.Error(w, fmt.Sprintf("Could not parse namespace: %v", err), http.StatusBadRequest)
		return
	}

    switch admissionReview.Request.Operation {
        case admissionv1.Create:
			logger.Info(fmt.Sprintf("Namespace %s created", namespace.Name))
		case admissionv1.Update:
			logger.Info(fmt.Sprintf("Namespace %s updated", namespace.Name))
            err = json.Unmarshal(admissionReview.Request.OldObject.Raw, &oldNamespace)
            if err != nil {
                logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse old namespace")
                http.Error(w, fmt.Sprintf("Could not parse old namespace: %v", err), http.StatusBadRequest)
                return
            }
		case admissionv1.Delete:
			logger.Info(fmt.Sprintf("Namespace %s deleted", namespace.Name))
            err = json.Unmarshal(admissionReview.Request.OldObject.Raw, &oldNamespace)
            if err != nil {
                logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse old namespace")
                http.Error(w, fmt.Sprintf("Could not parse old namespace: %v", err), http.StatusBadRequest)
                return
            }
		default:
            logger.Info("Unknown operation")
			return
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("failed to get in-cluster config")
		http.Error(w, fmt.Sprintf("Could not get in-cluster config: %v", err), http.StatusInternalServerError)
		return
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("failed to create clientset")
		http.Error(w, fmt.Sprintf("Could not create clientset: %v", err), http.StatusInternalServerError)
		return
	}

    targetName, targetKey := namespace.Labels["namespace.oam.dev/target"]
	runtime, runtimeKey := namespace.Labels["usage.oam.dev/runtime"]
    projectName := "test-project"

	switch admissionReview.Request.Operation {
        case admissionv1.Create:
            if targetKey && targetName != "" && runtimeKey && runtime == "target" {
                cronExpression := "@every 5m"
                err := createVeleroSchedule(*r, dynamicClient, projectName, targetName, namespace.Name, cronExpression, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to create Velero schedule")
                    http.Error(w, fmt.Sprintf("Failed to create Velero schedule: %v", err), http.StatusInternalServerError)
                    return
                }
                err = createVeleroBackup(*r, dynamicClient,  projectName, targetName, namespace.Name, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to create Velero backup")
                    http.Error(w, fmt.Sprintf("Failed to create Velero backup: %v", err), http.StatusInternalServerError)
                    return
                }
            }  
        case admissionv1.Update:
            oldTargetName, oldTargetKey := oldNamespace.Labels["namespace.oam.dev/target"]
            oldRuntime, oldRuntimeKey := oldNamespace.Labels["usage.oam.dev/runtime"]
            if targetKey && targetName != "" && runtimeKey && runtime == "target" && (!oldTargetKey || oldTargetName == "")  && (!oldRuntimeKey || oldRuntime == "") {
                cronExpression := "@every 5m"
                err := createVeleroSchedule(*r, dynamicClient, projectName, targetName, namespace.Name, cronExpression, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to create Velero schedule")
                    http.Error(w, fmt.Sprintf("Failed to create Velero schedule: %v", err), http.StatusInternalServerError)
                    return
                }
                err = createVeleroBackup(*r, dynamicClient,  projectName, targetName, namespace.Name, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to create Velero backup")
                    http.Error(w, fmt.Sprintf("Failed to create Velero backup: %v", err), http.StatusInternalServerError)
                    return
                }
            } else if (!targetKey || targetName == "" || !runtimeKey || runtime != "target") && oldTargetKey && oldTargetName != "" && oldRuntimeKey && oldRuntime == "target" {
                err := deleteVeleroSchedule(*r, dynamicClient, projectName, targetName, namespace.Name, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to delete Velero schedule")
                    http.Error(w, fmt.Sprintf("Failed to delete Velero schedule: %v", err), http.StatusInternalServerError)
                    return
                }
            }
            
            
        case admissionv1.Delete:
            if targetKey && targetName != "" && runtimeKey && runtime == "target" {
                err := deleteVeleroSchedule(*r, dynamicClient, projectName, targetName, namespace.Name, logger)
                if err != nil {
                    logger.WithFields(logrus.Fields{"error": err}).Error("Failed to delete Velero schedule")
                    http.Error(w, fmt.Sprintf("Failed to delete Velero schedule: %v", err), http.StatusInternalServerError)
                    return
                }
            }
    }

    response := admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			Allowed: true,
			UID: admissionReview.Request.UID,
		},
	}

	respBytes, err := json.Marshal(response)
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to marshal response")
		http.Error(w, fmt.Sprintf("Could not marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func createVeleroSchedule(r http.Request, client dynamic.Interface, projectName string, targetName string, namespaceName string, cronExpression string, logger *logrus.Entry) error {
	scheduleName := fmt.Sprintf("%s-%s-%s", projectName, targetName, namespaceName)
    
    veleroScheduleResource := schema.GroupVersionResource{
		Group: "velero.io",
		Version: "v1",
		Resource: "schedules",
	}

	_, err := client.Resource(veleroScheduleResource).Namespace("velero").Get(r.Context(), scheduleName, metav1.GetOptions{})
	if err != nil {
		logger.Info(fmt.Sprintf("Velero schedule %s already exists", scheduleName))
		return nil
	}

	veleroSchedule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind": "Schedule",
			"metadata": map[string]interface{}{
				"name": scheduleName,
				"namespace": "velero",
			},
			"spec": map[string]interface{}{
				"schedule": cronExpression,
				"useOwnerReferencesInBackup": false,
				"template": map[string]interface{}{
					"csiSnapshotTimeout": "10m",
					"includedNamespaces": []string{namespaceName},
					"storageLocation": "default",
					"ttl": "720h0m0s",
					"defaultVolumesToFsBackup": true,
				},
			},
		},
	}

	logger.Info(fmt.Sprintf("Creating Velero schedule %s", scheduleName))
	_, err = client.Resource(veleroScheduleResource).Namespace("velero").Create(r.Context(), veleroSchedule, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func createVeleroBackup(r http.Request, client dynamic.Interface, projectName string, targetName string, namespaceName string, logger *logrus.Entry) error {
	scheduleName := fmt.Sprintf("%s-%s-%s", projectName, targetName, namespaceName)
    backupName := fmt.Sprintf("%s-%s", scheduleName, time.Now().Format("20060102150405"))

    veleroBackupResource := schema.GroupVersionResource{
		Group: "velero.io",
		Version: "v1",
		Resource: "backups",
	}

	veleroBackup := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind": "Backup",
			"metadata": map[string]interface{}{
				"name": backupName,
				"namespace": "velero",
			},
			"spec": map[string]interface{}{
				"csiSnapshotTimeout": "10m",
				"itemOperationTimeout": "4h",
				"includedNamespaces": []string{namespaceName},
				"storageLocation": "default",
				"ttl": "720h0m0s",
				"defaultVolumesToFsBackup": true,
			},
		},
	}

	logger.Info(fmt.Sprintf("Creating Velero backup %s", backupName))
	_, err := client.Resource(veleroBackupResource).Namespace("velero").Create(r.Context(), veleroBackup, metav1.CreateOptions{})
	if err != nil {
		return err
	}

    return nil
}

func deleteVeleroSchedule(r http.Request, client dynamic.Interface, projectName string, targetName string, namespaceName string, logger *logrus.Entry) error {
    scheduleName := fmt.Sprintf("%s-%s-%s", projectName, targetName, namespaceName)

	veleroScheduleResource := schema.GroupVersionResource{
		Group: "velero.io",
		Version: "v1",
		Resource: "schedules",
	}

	logger.Info(fmt.Sprintf("Deleting Velero schedule %s", scheduleName))
	err := client.Resource(veleroScheduleResource).Namespace("velero").Delete(r.Context(), scheduleName, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

func ServerHealth(w http.ResponseWriter, r *http.Request) {
	logrus.WithFields(logrus.Fields{"uri": r.RequestURI}).Debug("healthy")
	w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
}

func setLogger() {
	logrus.SetLevel(logrus.DebugLevel)

	logLevel := os.Getenv("LOG_LEVEL")

	if logLevel != "" {
		level, err := logrus.ParseLevel(logLevel)
		if err != nil {
			logrus.Fatalf("Error setting log level: %v", err)
		}
		logrus.SetLevel(level)
	}

	if os.Getenv("LOG_FORMAT") == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	}
}

func parseRequest(r http.Request) (*admissionv1.AdmissionReview, error) {
	logrus.Debug("Parsing request")

	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("Content-Type: %q should be %q", r.Header.Get("Content-Type"), "application/json")
	}

	bodybuf := new(bytes.Buffer)
	bodybuf.ReadFrom(r.Body)
	body := bodybuf.Bytes()

	if len(body) == 0 {
		return nil, fmt.Errorf("Admission request body is empty")
	}

	var a admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("Failed to parse admission request: %v", err)
	}

	if a.Request == nil {
		return nil, fmt.Errorf("Admission request is empty")
	}

	return &a, nil
}