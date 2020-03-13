// Copyright 2019 Istio Authors.
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

package multicluster

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd/api"

	"istio.io/istio/pkg/kube/secretcontroller"
)

const (
	testNamespace          = "istio-system-test"
	testServiceAccountName = "test-service-account"
	testKubeconfig         = "test-Kubeconfig"
	testContext            = "test-context"
	testNetwork            = "test-network"
)

func makeServiceAccount(secrets ...string) *v1.ServiceAccount {
	sa := &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceAccountName,
			Namespace: testNamespace,
		},
	}

	for _, secret := range secrets {
		sa.Secrets = append(sa.Secrets, v1.ObjectReference{
			Name:      secret,
			Namespace: testNamespace,
		})
	}

	return sa
}

func makeSecret(name, caData, token string) *v1.Secret {
	out := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{},
	}
	if len(caData) > 0 {
		out.Data[v1.ServiceAccountRootCAKey] = []byte(caData)
	}
	if len(token) > 0 {
		out.Data[v1.ServiceAccountTokenKey] = []byte(token)
	}
	return out
}

type fakeOutputWriter struct {
	b           bytes.Buffer
	injectError error
	failAfter   int
}

func (w *fakeOutputWriter) Write(p []byte) (n int, err error) {
	w.failAfter--
	if w.failAfter <= 0 && w.injectError != nil {
		return 0, w.injectError
	}
	return w.b.Write(p)
}
func (w *fakeOutputWriter) String() string { return w.b.String() }

func TestCreateRemoteSecrets(t *testing.T) {
	prevOutputWriterStub := makeOutputWriterTestHook
	defer func() { makeOutputWriterTestHook = prevOutputWriterStub }()

	sa := makeServiceAccount("saSecret")
	saSecret := makeSecret("saSecret", "caData", "token")
	saSecretMissingToken := makeSecret("saSecret", "caData", "")
	badStartingConfigErrStr := "could not find cluster for context"

	wantOutput := `# This file is autogenerated, do not edit.
apiVersion: v1
kind: Secret
metadata:
  annotations:
    istio.io/clusterContext: test-context
  creationTimestamp: null
  labels:
    istio/multiCluster: "true"
  name: istio-remote-secret-54643f96-eca0-11e9-bb97-42010a80000a
  namespace: istio-system-test
stringData:
  54643f96-eca0-11e9-bb97-42010a80000a: |
    apiVersion: v1
    clusters:
    - cluster:
        certificate-authority-data: Y2FEYXRh
        server: server
      name: test-context
    contexts:
    - context:
        cluster: test-context
        user: test-context
      name: test-context
    current-context: test-context
    kind: Config
    preferences: {}
    users:
    - name: test-context
      user:
        token: token
---
`

	cases := []struct {
		testName string

		// test input
		config *api.Config
		objs   []runtime.Object
		name   string

		// inject errors
		badStartingConfig bool
		outputWriterError error

		want       string
		wantErrStr string
	}{
		{
			testName:   "fail to get service account secret token",
			objs:       []runtime.Object{kubeSystemNamespace, sa},
			wantErrStr: fmt.Sprintf("secrets %q not found", saSecret.Name),
		},
		{
			testName:          "fail to create starting config",
			objs:              []runtime.Object{kubeSystemNamespace, sa, saSecret},
			config:            api.NewConfig(),
			badStartingConfig: true,
			wantErrStr:        badStartingConfigErrStr,
		},
		{
			testName: "fail to find cluster in local Kubeconfig",
			objs:     []runtime.Object{kubeSystemNamespace, sa, saSecret},
			config: &api.Config{
				CurrentContext: testContext,
				Clusters:       map[string]*api.Cluster{ /* missing cluster */ },
			},
			wantErrStr: fmt.Sprintf(`could not find cluster for context %q`, testContext),
		},
		{
			testName: "fail to create remote secret token",
			objs:     []runtime.Object{kubeSystemNamespace, sa, saSecretMissingToken},
			config: &api.Config{
				CurrentContext: testContext,
				Contexts: map[string]*api.Context{
					testContext: {Cluster: "cluster"},
				},
				Clusters: map[string]*api.Cluster{
					"cluster": {Server: "server"},
				},
			},
			wantErrStr: `no "token" data found`,
		},
		{
			testName: "fail to encode secret",
			objs:     []runtime.Object{kubeSystemNamespace, sa, saSecret},
			config: &api.Config{
				CurrentContext: testContext,
				Contexts: map[string]*api.Context{
					testContext: {Cluster: "cluster"},
				},
				Clusters: map[string]*api.Cluster{
					"cluster": {Server: "server"},
				},
			},
			outputWriterError: errors.New("injected encode error"),
			wantErrStr:        "injected encode error",
		},
		{
			testName: "success",
			objs:     []runtime.Object{kubeSystemNamespace, sa, saSecret},
			config: &api.Config{
				CurrentContext: testContext,
				Contexts: map[string]*api.Context{
					testContext: {Cluster: "cluster"},
				},
				Clusters: map[string]*api.Cluster{
					"cluster": {Server: "server"},
				},
			},
			name: "cluster-foo",
			want: wantOutput,
		},
	}

	for i := range cases {
		c := &cases[i]
		t.Run(fmt.Sprintf("[%v] %v", i, c.testName), func(tt *testing.T) {
			makeOutputWriterTestHook = func() writer {
				return &fakeOutputWriter{injectError: c.outputWriterError}
			}

			opts := RemoteSecretOptions{
				ServiceAccountName: testServiceAccountName,
				AuthType:           RemoteSecretAuthTypeBearerToken,
				KubeOptions: KubeOptions{
					Namespace:  testNamespace,
					Context:    testContext,
					Kubeconfig: testKubeconfig,
				},
			}

			env := newFakeEnvironmentOrDie(t, c.config, c.objs...)

			got, err := CreateRemoteSecret(opts, env) // TODO
			if c.wantErrStr != "" {
				if err == nil {
					tt.Fatalf("wanted error including %q but got none", c.wantErrStr)
				} else if !strings.Contains(err.Error(), c.wantErrStr) {
					tt.Fatalf("wanted error including %q but got %v", c.wantErrStr, err)
				}
			} else if c.wantErrStr == "" && err != nil {
				tt.Fatalf("wanted non-error but got %q", err)
			} else if diff := cmp.Diff(got, c.want); diff != "" {
				tt.Errorf("got\n%v\nwant\n%vdiff %v", got, c.want, diff)
			}
		})
	}
}

func TestGetServiceAccountSecretToken(t *testing.T) {
	secret := makeSecret("secret", "caData", "token")

	cases := []struct {
		name string

		saNamespace string
		saName      string
		objs        []runtime.Object

		want       *v1.Secret
		wantErrStr string
	}{
		{
			name:        "missing service account",
			saName:      testServiceAccountName,
			saNamespace: testNamespace,
			wantErrStr:  fmt.Sprintf("serviceaccounts %q not found", testServiceAccountName),
		},
		{
			name:        "wrong number of secrets",
			saName:      testServiceAccountName,
			saNamespace: testNamespace,
			objs: []runtime.Object{
				makeServiceAccount("secret", "extra-secret"),
			},
			wantErrStr: "wrong number of secrets",
		},
		{
			name:        "missing service account token secret",
			saName:      testServiceAccountName,
			saNamespace: testNamespace,
			objs: []runtime.Object{
				makeServiceAccount("wrong-secret"),
				secret,
			},
			wantErrStr: `secrets "wrong-secret" not found`,
		},
		{
			name:        "success",
			saName:      testServiceAccountName,
			saNamespace: testNamespace,
			objs: []runtime.Object{
				makeServiceAccount("secret"),
				secret,
			},
			want: secret,
		},
	}

	for i := range cases {
		c := &cases[i]
		t.Run(fmt.Sprintf("[%v] %v", i, c.name), func(tt *testing.T) {
			kube := fake.NewSimpleClientset(c.objs...)

			got, err := getServiceAccountSecretToken(kube, c.saName, c.saNamespace)
			if c.wantErrStr != "" {
				if err == nil {
					tt.Fatalf("wanted error including %q but got none", c.wantErrStr)
				} else if !strings.Contains(err.Error(), c.wantErrStr) {
					tt.Fatalf("wanted error including %q but got %v", c.wantErrStr, err)
				}
			} else if c.wantErrStr == "" && err != nil {
				tt.Fatalf("wanted non-error but got %q", err)
			} else if diff := cmp.Diff(got, c.want); diff != "" {
				tt.Errorf("got\n%v\nwant\n%vdiff %v", got, c.want, diff)
			}
		})
	}
}

func TestGetClusterServerFromKubeconfig(t *testing.T) {
	wantServer := "server0"
	wantContext := "context0"
	context := "context0"
	cluster := "cluster0"

	cases := []struct {
		name       string
		config     *api.Config
		context    string
		wantErrStr string
	}{
		{
			name:       "bad starting config",
			context:    context,
			config:     api.NewConfig(),
			wantErrStr: "could not find cluster for context",
		},
		{
			name:    "missing cluster",
			context: context,
			config: &api.Config{
				CurrentContext: context,
				Contexts:       map[string]*api.Context{},
				Clusters:       map[string]*api.Cluster{},
			},
			wantErrStr: "could not find cluster for context",
		},
		{
			name:    "missing server",
			context: context,
			config: &api.Config{
				CurrentContext: context,
				Contexts: map[string]*api.Context{
					context: {Cluster: cluster},
				},
				Clusters: map[string]*api.Cluster{},
			},
			wantErrStr: "could not find server for context",
		},
		{
			name:    "success",
			context: context,
			config: &api.Config{
				CurrentContext: context,
				Contexts: map[string]*api.Context{
					context: {Cluster: cluster},
				},
				Clusters: map[string]*api.Cluster{
					cluster: {Server: wantServer},
				},
			},
		},
		{
			name:    "use explicit Context different from current-context",
			context: context,
			config: &api.Config{
				CurrentContext: "ignored-context", // verify context override is used
				Contexts: map[string]*api.Context{
					context: {Cluster: cluster},
				},
				Clusters: map[string]*api.Cluster{
					cluster: {Server: wantServer},
				},
			},
		},
	}

	for i := range cases {
		c := &cases[i]
		t.Run(fmt.Sprintf("[%v] %v", i, c.name), func(tt *testing.T) {
			gotContext, gotServer, err := getCurrentContextAndClusterServerFromKubeconfig(c.context, c.config)
			if c.wantErrStr != "" {
				if err == nil {
					tt.Fatalf("wanted error including %q but got none", c.wantErrStr)
				} else if !strings.Contains(err.Error(), c.wantErrStr) {
					tt.Fatalf("wanted error including %q but got %v", c.wantErrStr, err)
				}
			} else if c.wantErrStr == "" && err != nil {
				tt.Fatalf("wanted non-error but got %q", err)
			} else {
				if gotServer != wantServer {
					t.Errorf("got server %v want %v", gotServer, wantServer)
				}
				if gotContext != wantContext {
					t.Errorf("got Context %v want %v", gotContext, wantContext)
				}
			}
		})
	}
}

func TestCreateRemoteKubeconfig(t *testing.T) {
	kubeconfig := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Y2FEYXRh
    server: ""
  name: c0
contexts:
- context:
    cluster: c0
    user: c0
  name: c0
current-context: c0
kind: Config
preferences: {}
users:
- name: c0
  user:
    token: token
`

	fakeClusterName := "fake-clusterName-0"
	cases := []struct {
		name        string
		clusterName string
		context     string
		server      string
		in          *v1.Secret
		want        *v1.Secret
		wantErrStr  string
	}{
		{
			name:        "missing caData",
			in:          makeSecret("", "", "token"),
			context:     "c0",
			clusterName: fakeClusterName,
			wantErrStr:  errMissingRootCAKey.Error(),
		},
		{
			name:        "missing token",
			in:          makeSecret("", "caData", ""),
			context:     "c0",
			clusterName: fakeClusterName,
			wantErrStr:  errMissingTokenKey.Error(),
		},
		{
			name:        "success",
			in:          makeSecret("", "caData", "token"),
			context:     "c0",
			clusterName: fakeClusterName,
			want: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: remoteSecretNameFromClusterName(fakeClusterName),
					Annotations: map[string]string{
						"istio.io/clusterContext": "c0",
					},
					Labels: map[string]string{
						secretcontroller.MultiClusterSecretLabel: "true",
					},
				},
				Data: map[string][]byte{
					fakeClusterName: []byte(kubeconfig),
				},
			},
		},
	}

	for i := range cases {
		c := &cases[i]
		t.Run(fmt.Sprintf("[%v] %v", i, c.name), func(tt *testing.T) {
			got, err := createRemoteSecretFromTokenAndServer(c.in, c.clusterName, c.context, c.server)
			if c.wantErrStr != "" {
				if err == nil {
					tt.Fatalf("wanted error including %q but none", c.wantErrStr)
				} else if !strings.Contains(err.Error(), c.wantErrStr) {
					tt.Fatalf("wanted error including %q but %v", c.wantErrStr, err)
				}
			} else if c.wantErrStr == "" && err != nil {
				tt.Fatalf("wanted non-error but got %q", err)
			} else if diff := cmp.Diff(got, c.want); diff != "" {
				tt.Fatalf(" got %v\nwant %v\ndiff %v", got, c.want, diff)
			}
		})
	}
}

func TestWriteEncodedSecret(t *testing.T) {
	s := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}

	w := &fakeOutputWriter{failAfter: 0, injectError: errors.New("error")}
	if err := writeEncodedObject(w, s); err == nil {
		t.Error("want error on local write failure")
	}

	w = &fakeOutputWriter{failAfter: 1, injectError: errors.New("error")}
	if err := writeEncodedObject(w, s); err == nil {
		t.Error("want error on remote write failure")
	}

	w = &fakeOutputWriter{failAfter: 2, injectError: errors.New("error")}
	if err := writeEncodedObject(w, s); err == nil {
		t.Error("want error on third write failure")
	}

	w = &fakeOutputWriter{}
	if err := writeEncodedObject(w, s); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	want := `# This file is autogenerated, do not edit.
apiVersion: v1
kind: Secret
metadata:
  creationTimestamp: null
  name: foo
---
`
	if w.String() != want {
		t.Errorf("got\n%q\nwant\n%q", w.String(), want)
	}

}

func TestCreateRemoteSecretFromPlugin(t *testing.T) {
	kubeconfig := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Y2FEYXRh
    server: ""
  name: c0
contexts:
- context:
    cluster: c0
    user: c0
  name: c0
current-context: c0
kind: Config
preferences: {}
users:
- name: c0
  user:
    auth-provider:
      config:
        k1: v1
      name: foobar
`
	fakeClusterName := "fake-clusterName-0"

	cases := []struct {
		name               string
		in                 *v1.Secret
		context            string
		clusterName        string
		server             string
		authProviderConfig *api.AuthProviderConfig
		want               *v1.Secret
		wantErrStr         string
	}{
		{
			name:        "error on missing caData",
			in:          makeSecret("", "", "token"),
			context:     "c0",
			clusterName: fakeClusterName,
			wantErrStr:  errMissingRootCAKey.Error(),
		},
		{
			name:        "success on missing token",
			in:          makeSecret("", "caData", ""),
			context:     "c0",
			clusterName: fakeClusterName,
			authProviderConfig: &api.AuthProviderConfig{
				Name: "foobar",
				Config: map[string]string{
					"k1": "v1",
				},
			},
			want: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: remoteSecretNameFromClusterName(fakeClusterName),
					Annotations: map[string]string{
						"istio.io/clusterContext": "c0",
					},
					Labels: map[string]string{
						secretcontroller.MultiClusterSecretLabel: "true",
					},
				},
				Data: map[string][]byte{
					fakeClusterName: []byte(kubeconfig),
				},
			},
		},
		{
			name:        "success",
			in:          makeSecret("", "caData", "token"),
			context:     "c0",
			clusterName: fakeClusterName,
			authProviderConfig: &api.AuthProviderConfig{
				Name: "foobar",
				Config: map[string]string{
					"k1": "v1",
				},
			},
			want: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: remoteSecretNameFromClusterName(fakeClusterName),
					Annotations: map[string]string{
						"istio.io/clusterContext": "c0",
					},
					Labels: map[string]string{
						secretcontroller.MultiClusterSecretLabel: "true",
					},
				},
				Data: map[string][]byte{
					fakeClusterName: []byte(kubeconfig),
				},
			},
		},
	}

	for i := range cases {
		c := &cases[i]
		t.Run(fmt.Sprintf("[%v] %v", i, c.name), func(tt *testing.T) {
			got, err := createRemoteSecretFromPlugin(c.in, c.context, c.server, c.clusterName, c.authProviderConfig)
			if c.wantErrStr != "" {
				if err == nil {
					tt.Fatalf("wanted error including %q but none", c.wantErrStr)
				} else if !strings.Contains(err.Error(), c.wantErrStr) {
					tt.Fatalf("wanted error including %q but %v", c.wantErrStr, err)
				}
			} else if c.wantErrStr == "" && err != nil {
				tt.Fatalf("wanted non-error but got %q", err)
			} else if diff := cmp.Diff(got, c.want); diff != "" {
				tt.Fatalf(" got %v\nwant %v\ndiff %v", got, c.want, diff)
			}
		})
	}
}

func TestRemoteSecretOptions(t *testing.T) {
	g := NewGomegaWithT(t)

	o := RemoteSecretOptions{}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	o.addFlags(flags)
	g.Expect(flags.Parse([]string{
		"--name",
		"valid-name",
	})).Should(Succeed())
	g.Expect(o.prepare(flags)).Should(Succeed())

	o = RemoteSecretOptions{}
	flags = pflag.NewFlagSet("test", pflag.ContinueOnError)
	o.addFlags(flags)
	g.Expect(flags.Parse([]string{
		"--name",
		"?-invalid-name",
	})).Should(Succeed())
	g.Expect(o.prepare(flags)).Should(Not(Succeed()))
}
