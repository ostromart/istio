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
	"fmt"
	"net"
	"net/http"

	restful "github.com/emicklei/go-restful"
	_ "github.com/golang/glog" // TODO(nmittler): Remove this
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/proxy/envoy/v1"
)

// TODO(mostrowski): This is a hack to remove the circular dependency between v1 and v2. Remove file once integration is done.

// DiscoveryService publishes services, clusters, and routes for all proxies
type DiscoveryService struct {
	dsV1 *v1.DiscoveryService
	ws   *restful.WebService
	model.Environment
}

// NewDiscoveryService creates an Envoy discovery service on a given port
func NewDiscoveryService(ctl model.Controller, configCache model.ConfigStoreCache,
	environment model.Environment, o v1.DiscoveryServiceOptions) (*DiscoveryService, error) {

	out := &DiscoveryService{Environment: environment}
	container := restful.NewContainer()
	out.Register(container)

	ds1, err := v1.NewDiscoveryService(ctl, configCache, environment, o, container, out.ws)
	if err != nil {
		return nil, nil
	}

	out.dsV1 = ds1

	return out, nil
}

// Register adds routes a web service container. This is visible for testing purposes only.
func (ds *DiscoveryService) Register(container *restful.Container) {
	ws := &restful.WebService{}
	ws.Produces(restful.MIME_JSON)

	// This route responds to LDS requests
	// See https://lyft.github.io/envoy/docs/configuration/listeners/lds.html
	ws.Route(ws.
		GET(fmt.Sprintf("/v2/listeners/{%s}/{%s}", v1.ServiceCluster, v1.ServiceNode)).
		To(ds.ListListenersV2).
		Doc("LDS registration").
		Param(ws.PathParameter(v1.ServiceCluster, "client proxy service cluster").DataType("string")).
		Param(ws.PathParameter(v1.ServiceNode, "client proxy service node").DataType("string")))

	ds.ws = ws
}

func (ds *DiscoveryService) Start(stop chan struct{}) (net.Addr, error) {
	return ds.dsV1.Start(stop)
}

// GetCacheStats returns the statistics for cached discovery responses.
func (ds *DiscoveryService) GetCacheStats(_ *restful.Request, response *restful.Response) {
	ds.dsV1.GetCacheStats(nil, response)
}

// ListAllEndpoints responds with all Services and is not restricted to a single service-key
func (ds *DiscoveryService) ListAllEndpoints(_ *restful.Request, response *restful.Response) {
	ds.dsV1.ListAllEndpoints(nil, response)
}

// ListEndpoints responds to EDS requests
func (ds *DiscoveryService) ListEndpoints(request *restful.Request, response *restful.Response) {
	ds.dsV1.ListEndpoints(request, response)
}

// AvailabilityZone responds to requests for an AZ for the given cluster node
func (ds *DiscoveryService) AvailabilityZone(request *restful.Request, response *restful.Response) {
	ds.dsV1.AvailabilityZone(request, response)
}

// ListClusters responds to CDS requests for all outbound clusters
func (ds *DiscoveryService) ListClusters(request *restful.Request, response *restful.Response) {
	ds.dsV1.ListClusters(request, response)
}

// ListListeners responds to LDS requests
func (ds *DiscoveryService) ListListeners(request *restful.Request, response *restful.Response) {
	ds.dsV1.ListListeners(request, response)
}

// ListListenersV2 responds to LDS V2 requests
func (ds *DiscoveryService) ListListenersV2(request *restful.Request, response *restful.Response) {
	methodName := "ListListenersV2"
	v1.IncCalls(methodName)

	svcNode, err := ds.dsV1.ParseDiscoveryRequest(request)
	if err != nil {
		v1.ErrorResponse(methodName, response, http.StatusNotFound, "LDS "+err.Error())
		return
	}

	out, err := ListListenersResponse(ds.Environment, svcNode)
	if err != nil {
		v1.ErrorResponse(methodName, response, http.StatusInternalServerError, "LDS "+err.Error())
		return
	}

	// TODO: add webhook
	resourceCount := uint32(len(out.Resources))
	v1.ObserveResources(methodName, resourceCount)
	v1.WriteResponse(response, []byte(out.String()))
}

func (ds *DiscoveryService) ListRoutes(request *restful.Request, response *restful.Response) {
	ds.dsV1.ListRoutes(request, response)
}
