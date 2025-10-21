package lvm

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func Test_deleteVolume(t *testing.T) {
	tests := []struct {
		name                         string
		volID                        string
		volumeExists                 bool
		expectedProvisionerPodStatus v1.PodPhase
		wantResponse                 *csi.DeleteVolumeResponse
		wantErr                      bool
	}{
		{
			name:                         "delete volume",
			volID:                        "vol-123",
			volumeExists:                 true,
			expectedProvisionerPodStatus: v1.PodSucceeded,
			wantResponse:                 &csi.DeleteVolumeResponse{},
			wantErr:                      false,
		},
		{
			name:                         "delete volume not found",
			volID:                        "vol-456",
			volumeExists:                 false,
			expectedProvisionerPodStatus: "", // non-existent volume will return early
			wantResponse:                 &csi.DeleteVolumeResponse{},
			wantErr:                      false,
		},
		{
			name:                         "delete volume error",
			volID:                        "vol-789",
			volumeExists:                 true,
			expectedProvisionerPodStatus: v1.PodFailed,
			wantResponse:                 nil,
			wantErr:                      true,
		},
		{
			name:         "delete volume with non-existent vg",
			volID:        "vol-nonexist-vg",
			volumeExists: true,
			// Note: this test case is not validating inner behavior of provisionerPod.
			// Intstead, it is simply expecting the provisioner pod status to be Succeeded when VG does not exist.
			// The actual test should be done in lvm.go tests.
			expectedProvisionerPodStatus: v1.PodSucceeded,
			wantResponse:                 &csi.DeleteVolumeResponse{},
			wantErr:                      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// fake client for controller
			cs := &controllerServer{
				caps: getControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					}),
				nodeID:     "node-123",
				kubeClient: fake.NewSimpleClientset(),
			}

			// mock volume creation
			if tt.volumeExists {
				// Create a mock node first
				_, err := cs.kubeClient.CoreV1().Nodes().Create(context.Background(), &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				}, metav1.CreateOptions{})
				if err != nil {
					t.Errorf("CreateNode() error = %v", err)
					return
				}

				// Create the PersistentVolume with proper node affinity
				_, err = cs.kubeClient.CoreV1().PersistentVolumes().Create(context.Background(), &v1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.volID,
					},
					Spec: v1.PersistentVolumeSpec{
						NodeAffinity: &v1.VolumeNodeAffinity{
							Required: &v1.NodeSelector{
								NodeSelectorTerms: []v1.NodeSelectorTerm{
									{
										MatchExpressions: []v1.NodeSelectorRequirement{
											{
												Key:    "kubernetes.io/hostname",
												Values: []string{"test-node"},
											},
										},
									},
								},
							},
						},
					},
				}, metav1.CreateOptions{})
				if err != nil {
					t.Errorf("CreatePersistentVolume() error = %v", err)
					return
				}
			}

			if tt.expectedProvisionerPodStatus != "" {
				podStatusUpdated := make(chan struct{})
				go func() {
					defer close(podStatusUpdated)
					// poll to check if the provisioner pod is created. if it is, set the status to Succeeded
					if tt.volumeExists {
						for {
							pod, err := cs.kubeClient.CoreV1().Pods(cs.namespace).Get(context.Background(), "delete-"+tt.volID, metav1.GetOptions{})
							if k8serror.IsNotFound(err) {
								klog.Infof("pod %s not found, waiting for it to be created", "delete-"+tt.volID)
								time.Sleep(3 * time.Second)
								continue
							} else if err != nil {
								t.Errorf("GetPod() error = %v", err)
								return
							}
							// Update the pod status to the test target
							pod.Status.Phase = tt.expectedProvisionerPodStatus
							klog.Infof("pod %s status updated to Succeeded", pod.Name)
							_, err = cs.kubeClient.CoreV1().Pods(cs.namespace).Update(context.Background(), pod, metav1.UpdateOptions{})
							if err != nil {
								t.Errorf("UpdatePod() error = %v", err)
								return
							}
							break
						}
					}
				}()
			}
			// The test target
			got, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
				VolumeId: tt.volID,
			})
			if !reflect.DeepEqual(got, tt.wantResponse) {
				t.Errorf("DeleteVolume() = %v, wantResponse %v", got, tt.wantResponse)
				return
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

		})
	}
}
