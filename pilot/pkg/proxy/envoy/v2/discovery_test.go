// Copyright 2017 Istio Authors
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

package v2

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	restful "github.com/emicklei/go-restful"
	meshconfig "istio.io/api/mesh/v1alpha1"
	"istio.io/istio/pilot/pkg/config/memory"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/proxy/envoy/v1"
	"istio.io/istio/pilot/pkg/proxy/envoy/v1/mock"
	"istio.io/istio/pilot/test/util"
)

// Implement minimal methods to satisfy model.Controller interface for
// creating a new discovery service instance.
type mockController struct {
	handlers int
}

func (ctl *mockController) AppendServiceHandler(_ func(*model.Service, model.Event)) error {
	ctl.handlers++
	return nil
}
func (ctl *mockController) AppendInstanceHandler(_ func(*model.ServiceInstance, model.Event)) error {
	ctl.handlers++
	return nil
}
func (ctl *mockController) Run(_ <-chan struct{}) {}

var mockDiscovery *mock.ServiceDiscovery

func makeDiscoveryService(t *testing.T, r model.ConfigStore, mesh *meshconfig.MeshConfig) *DiscoveryService {
	mockDiscovery = mock.Discovery
	mockDiscovery.ClearErrors()
	out, err := NewDiscoveryService(
		&mockController{},
		nil,
		model.Environment{
			ServiceDiscovery: mockDiscovery,
			ServiceAccounts:  mockDiscovery,
			IstioConfigStore: model.MakeIstioStore(r),
			Mesh:             mesh,
		},
		v1.DiscoveryServiceOptions{
			EnableCaching:   true,
			EnableProfiling: true, // increase code coverage stats
		})
	if err != nil {
		t.Fatalf("NewDiscoveryService failed: %v", err)
	}
	return out
}

func makeDiscoveryRequest(ds *DiscoveryService, method, url string, t *testing.T) []byte {
	httpRequest, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpWriter := httptest.NewRecorder()
	container := restful.NewContainer()
	ds.Register(container)
	container.ServeHTTP(httpWriter, httpRequest)
	body, err := ioutil.ReadAll(httpWriter.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func getDiscoveryResponse(ds *DiscoveryService, method, url string, t *testing.T) *http.Response {
	httpRequest, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpWriter := httptest.NewRecorder()
	container := restful.NewContainer()
	ds.Register(container)
	container.ServeHTTP(httpWriter, httpRequest)

	return httpWriter.Result()
}

func commonSetup(t *testing.T) (*meshconfig.MeshConfig, model.ConfigStore, *DiscoveryService) {
	mesh := makeMeshConfig()
	registry := memory.Make(model.IstioConfigTypes)
	ds := makeDiscoveryService(t, registry, &mesh)
	return &mesh, registry, ds
}

func compareResponse(body []byte, file string, t *testing.T) {
	err := ioutil.WriteFile(file, body, 0644)
	if err != nil {
		t.Fatalf(err.Error())
	}
	util.CompareYAML(file, t)
}

func addIngressRoutes(r model.ConfigStore, t *testing.T) {
	addConfig(r, ingressRouteRule1, t)
	addConfig(r, ingressRouteRule2, t)
}

func TestRouteDiscoveryWebsocket(t *testing.T) {
	for _, websocketConfig := range []fileConfig{websocketRouteRule, websocketRouteRuleV2} {
		_, registry, ds := commonSetup(t)
		addConfig(registry, websocketConfig, t)

		url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
		response := makeDiscoveryRequest(ds, "GET", url, t)
		compareResponse(response, "testdata/lds-websocket.json", t)
	}
}

func TestExternalServicesDiscoveryMode(t *testing.T) {
	testCases := []struct {
		name string
		file fileConfig
	}{
		{name: "http-none", file: externalServiceRule},
		{name: "http-dns", file: externalServiceRuleDNS},
		{name: "http-static", file: externalServiceRuleStatic},
		{name: "tcp-none", file: externalServiceRuleTCP},
		{name: "tcp-dns", file: externalServiceRuleTCPDNS},
		{name: "tcp-static", file: externalServiceRuleTCPStatic},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, registry, ds := commonSetup(t)

			if testCase.name != "none" {
				addConfig(registry, testCase.file, t)
			}

			url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
			response := makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v0-%s.json", testCase.name), t)
		})
	}
}

func TestExternalServicesRoutingRules(t *testing.T) {
	testCases := []struct {
		name  string
		files []fileConfig
	}{
		{name: "weighted-external-service", files: []fileConfig{externalServiceRuleStatic, destinationRuleExternal, externalServiceRouteRule}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, registry, ds := commonSetup(t)

			for _, file := range testCase.files {
				addConfig(registry, file, t)
			}

			url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
			response := makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v0-%s.json", testCase.name), t)
		})
	}
}

func TestListenerDiscoverySidecar(t *testing.T) {
	testCases := []struct {
		name string
		file fileConfig
	}{
		{name: "none"},
		/* these configs do not affect listeners
		{
			name: "cb",
			file: cbPolicy,
		},
		{
			name: "redirect",
			file: redirectRouteRule,
		},
		{
			name: "rewrite",
			file: rewriteRouteRule,
		},
		{
			name: "websocket",
			file: websocketRouteRule,
		},
		{
			name: "timeout",
			file: timeoutRouteRule,
		},
		*/
		{
			name: "weighted",
			file: weightedRouteRule,
		},
		{
			name: "weighted",
			file: weightedRouteRuleV2,
		},
		{
			name: "fault",
			file: faultRouteRule,
		},
		{
			name: "fault",
			file: faultRouteRuleV2,
		},
		{
			name: "multi-match-fault",
			file: multiMatchFaultRouteRuleV2,
		},
		{
			name: "egress-rule",
			file: egressRule,
		},
		{
			name: "egress-rule-tcp",
			file: egressRuleTCP,
		},
		{
			name: "egress-rule", // verify the output matches egress
			file: externalServiceRule,
		},
		{
			name: "egress-rule-tcp", // verify the output matches egress
			file: externalServiceRuleTCP,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, registry, ds := commonSetup(t)

			if testCase.name != "none" {
				addConfig(registry, destinationRuleWorld, t) // TODO: v1alpha2 only
				addConfig(registry, testCase.file, t)
			}

			// test with no auth
			url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
			response := makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v0-%s.json", testCase.name), t)

			url = fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV1.ServiceNode())
			response = makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v1-%s.json", testCase.name), t)

			// test with no mixer
			mesh := makeMeshConfig()
			mesh.MixerCheckServer = ""
			mesh.MixerReportServer = ""
			ds = makeDiscoveryService(t, registry, &mesh)
			url = fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
			response = makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v0-%s-nomixer.json", testCase.name), t)

			url = fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV1.ServiceNode())
			response = makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v1-%s-nomixer.json", testCase.name), t)

			// test with auth
			mesh = makeMeshConfig()
			mesh.AuthPolicy = meshconfig.MeshConfig_MUTUAL_TLS
			ds = makeDiscoveryService(t, registry, &mesh)
			url = fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
			response = makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v0-%s-auth.json", testCase.name), t)

			url = fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV1.ServiceNode())
			response = makeDiscoveryRequest(ds, "GET", url, t)
			compareResponse(response, fmt.Sprintf("testdata/lds-v1-%s-auth.json", testCase.name), t)
		})
	}
}

func TestListenerDiscoverySidecarAuthOptIn(t *testing.T) {
	mesh := makeMeshConfig()
	registry := memory.Make(model.IstioConfigTypes)

	// Auth opt-in on port 80
	mock.HelloService.Ports[0].AuthenticationPolicy = meshconfig.AuthenticationPolicy_MUTUAL_TLS
	ds := makeDiscoveryService(t, registry, &mesh)
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-v0-none-auth-optin.json", t)
	mock.HelloService.Ports[0].AuthenticationPolicy = meshconfig.AuthenticationPolicy_INHERIT
}

func TestListenerDiscoverySidecarAuthOptOut(t *testing.T) {
	mesh := makeMeshConfig()
	mesh.AuthPolicy = meshconfig.MeshConfig_MUTUAL_TLS
	registry := memory.Make(model.IstioConfigTypes)

	// Auth opt-out on port 80
	mock.HelloService.Ports[0].AuthenticationPolicy = meshconfig.AuthenticationPolicy_NONE
	ds := makeDiscoveryService(t, registry, &mesh)
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-v0-none-auth-optout.json", t)
	mock.HelloService.Ports[0].AuthenticationPolicy = meshconfig.AuthenticationPolicy_INHERIT
}

func TestRouteDiscoverySidecarError(t *testing.T) {
	_, _, ds := commonSetup(t)
	mockDiscovery.ServicesError = errors.New("mock Services() error")
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV1.ServiceNode())
	response := getDiscoveryResponse(ds, "GET", url, t)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unexpected error response from discovery: got %v, want %v",
			response.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestRouteDiscoverySidecarError2(t *testing.T) {
	_, _, ds := commonSetup(t)
	mockDiscovery.GetProxyServiceInstancesError = errors.New("mock GetProxyServiceInstances() error")
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV1.ServiceNode())
	response := getDiscoveryResponse(ds, "GET", url, t)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unexpected error response from discovery: got %v, want %v",
			response.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestListenerDiscoveryIngress(t *testing.T) {
	mesh := makeMeshConfig()
	registry := memory.Make(model.IstioConfigTypes)
	addConfig(registry, egressRule, t)
	addConfig(registry, egressRuleTCP, t)

	addIngressRoutes(registry, t)
	ds := makeDiscoveryService(t, registry, &mesh)
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.Ingress.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-ingress.json", t)

	mesh.AuthPolicy = meshconfig.MeshConfig_MUTUAL_TLS
	ds = makeDiscoveryService(t, registry, &mesh)
	response = makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-ingress.json", t)
}

func TestListenerDiscoverySidecarError(t *testing.T) {
	_, _, ds := commonSetup(t)
	mockDiscovery.ServicesError = errors.New("mock Services() error")
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := getDiscoveryResponse(ds, "GET", url, t)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unexpected error response from discovery: got %v, want %v",
			response.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestListenerDiscoverySidecarError2(t *testing.T) {
	_, _, ds := commonSetup(t)
	mockDiscovery.GetProxyServiceInstancesError = errors.New("mock GetProxyServiceInstances() error")
	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := getDiscoveryResponse(ds, "GET", url, t)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unexpected error response from discovery: got %v, want %v",
			response.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestListenerDiscoveryHttpProxy(t *testing.T) {
	mesh := makeMeshConfig()
	mesh.ProxyListenPort = 0
	mesh.ProxyHttpPort = 15002
	registry := memory.Make(model.IstioConfigTypes)
	ds := makeDiscoveryService(t, registry, &mesh)
	addConfig(registry, egressRule, t)

	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-httpproxy.json", t)
}

func TestListenerDiscoveryRouterWithGateway(t *testing.T) {
	mesh := makeMeshConfig()
	registry := memory.Make(model.IstioConfigTypes)
	ds := makeDiscoveryService(t, registry, &mesh)

	addConfig(registry, gatewayRouteRule, t)
	addConfig(registry, gatewayWeightedRouteRule, t)
	addConfig(registry, gatewayConfig, t)
	addConfig(registry, gatewayConfig2, t)
	addConfig(registry, destinationRuleWorld, t)

	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.Router.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-router-with-gateway.json", t)

	mesh.AuthPolicy = meshconfig.MeshConfig_MUTUAL_TLS
	ds = makeDiscoveryService(t, registry, &mesh)
	response = makeDiscoveryRequest(ds, "GET", url, t)

	// same response with or without auth
	compareResponse(response, "testdata/lds-router-with-gateway.json", t)
}

func TestMixerFilterServiceConfig(t *testing.T) {
	_, registry, ds := commonSetup(t)

	addConfig(registry, mixerclientAPISpec, t)
	addConfig(registry, mixerclientAPISpecBinding, t)
	addConfig(registry, mixerclientQuotaSpec, t)
	addConfig(registry, mixerclientQuotaSpecBinding, t)
	addConfig(registry, mixerclientAuthSpec, t)
	addConfig(registry, mixerclientAuthSpecBinding, t)

	url := fmt.Sprintf("/v2/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-mixerclient-filter.json", t)
}

func TestSeparateCheckReportClusters(t *testing.T) {
	mesh := makeMeshConfig()
	mesh.MixerCheckServer = "istio-mixer-policy-check.istio-system:9090"
	mesh.MixerReportServer = "istio-mixer-telemetry.istio-system:9090"
	registry := memory.Make(model.IstioConfigTypes)
	ds := makeDiscoveryService(t, registry, &mesh)

	url := fmt.Sprintf("/v1/listeners/%s/%s", "istio-proxy", mock.HelloProxyV0.ServiceNode())
	response := makeDiscoveryRequest(ds, "GET", url, t)
	compareResponse(response, "testdata/lds-mixer-check-report-config.json", t)
}
