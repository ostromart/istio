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
	"github.com/envoyproxy/go-control-plane/api"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"istio.io/istio/pilot/pkg/model"
)

var (
	// TODO(mostrowski): move this up to discovery
	LDSServerInstance *LDSServer
)

func NewLDSServer(env model.Environment) *LDSServer {
	return &LDSServer{
		env: env,
	}
}

func ChainLDSServer(server *grpc.Server, lds *LDSServer) {
	api.RegisterListenerDiscoveryServiceServer(server, lds)
	LDSServerInstance = lds
}

type LDSServer struct {
	env model.Environment
}

func (l *LDSServer) StreamListeners(api.ListenerDiscoveryService_StreamListenersServer) error {
	return nil
}

func (l *LDSServer) FetchListeners(ctx context.Context, in *api.DiscoveryRequest) (*api.DiscoveryResponse, error) {
	node, err := model.ParseServiceNode(in.Node.Id)
	if err != nil {
		return nil, err
	}
	return ListListenersResponse(l.env, node)
}
