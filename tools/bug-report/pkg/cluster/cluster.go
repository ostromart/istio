// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cluster

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"istio.io/pkg/log"
)

type ResourceType int

const (
	Namespace ResourceType = iota
	Deployment
	Pod
	Label
	Annotation
	Container
)

// GetClusterResources returns cluster resources for the given REST config and k8s Clientset.
func GetClusterResources(ctx context.Context, clientset *kubernetes.Clientset) (*Resources, error) {
	var errs []string
	out := &Resources{
		Labels:      make(map[string]map[string]string),
		Annotations: make(map[string]map[string]string),
		Pod:         make(map[string]*corev1.Pod),
	}
	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, ns := range namespaces.Items {
		pods, err := clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		replicasets, err := clientset.AppsV1().ReplicaSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, p := range pods.Items {
			deployment, err := getOwnerDeployment(&p, replicasets.Items)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			for _, c := range p.Spec.Containers {
				out.insertContainer(ns.Name, deployment, p.Name, c.Name)
			}
			out.Labels[p.Name] = p.Labels
			out.Annotations[p.Name] = p.Annotations
			out.Pod[p.Name] = &p
		}
	}
	if len(errs) != 0 {
		log.Warna(strings.Join(errs, "\n"))
	}
	return out, nil
}

// Resources defines a tree of cluster resource names.
type Resources struct {
	// Root is the first level in the cluster resource hierarchy.
	// Each level in the hierarchy is a map[string]interface{} to the next level.
	// The levels are: namespaces/deployments/pods/containers.
	Root map[string]interface{}
	// Labels maps a pod name to a map of labels key-values.
	Labels map[string]map[string]string
	// Annotations maps a pod name to a map of annotation key-values.
	Annotations map[string]map[string]string
	// Pod maps a pod name to its Pod info.
	Pod map[string]*corev1.Pod
}

func (r *Resources) insertContainer(namespace, deployment, pod, container string) {
	if r.Root == nil {
		r.Root = make(map[string]interface{})
	}
	if r.Root[namespace] == nil {
		r.Root[namespace] = make(map[string]interface{})
	}
	d := r.Root[namespace].(map[string]interface{})
	if d[deployment] == nil {
		d[deployment] = make(map[string]interface{})
	}
	p := d[deployment].(map[string]interface{})
	if p[pod] == nil {
		p[pod] = make(map[string]interface{})
	}
	c := p[pod].(map[string]interface{})
	c[container] = nil
}
			if strings.HasPrefix(pod, "istiod-") {
				wg2.Add(1)
				go func() {
					defer wg2.Done()
					info, err := content.GetIstiodInfo(namespace, pod, config.DryRun)
					lock.Lock()
					errs = util.AppendErr(errs, err)
					lock.Unlock()
					fmt.Println(info)
				}()
			}
func (r *Resources) ContainerRestarts(pod, container string) int {
	_, ok := r.Pod[pod]; if !ok {
		return 0
	}
	if len(r.Pod[pod].Status.ContainerStatuses) == 0 {
		return 0
	}
	for _, cs := range r.Pod[pod].Status.ContainerStatuses {
		if cs.Name == container {
			return int(cs.RestartCount)
		}
	}
	return 0
}

func (r *Resources) String() string {
	return resourcesStringImpl(r.Root, "")
}

func resourcesStringImpl(node interface{}, prefix string) string {
	out := ""
	if node == nil {
		return ""
	}
	nv := node.(map[string]interface{})
	for k, n := range nv {
		out += prefix + k + "\n"
		out += resourcesStringImpl(n, prefix+"  ")
	}

	return out
}

func getOwnerDeployment(pod *corev1.Pod, replicasets []v1.ReplicaSet) (string, error) {
	for _, o := range pod.OwnerReferences {
		if o.Kind == "ReplicaSet" {
			for _, rs := range replicasets {
				if rs.Name == o.Name {
					for _, oo := range rs.OwnerReferences {
						if oo.Kind == "Deployment" {
							return oo.Name, nil
						}
					}

				}
			}
		}
	}
	return "", fmt.Errorf("no owning Deployment found for pod %s", pod.Name)
}
