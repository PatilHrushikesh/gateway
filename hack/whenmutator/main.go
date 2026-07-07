// Minimal plaintext (h2c) ext_proc server used only for manual testing of the
// EnvoyExtensionPolicy `when` feature. It injects the `add-new-extproc` request
// header when the request carries `x-add-extproc: true`, simulating an earlier
// filter (e.g. an auth ext_proc) that conditionally adds a header consumed by a
// later conditional ext_proc.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

type server struct {
	extprocv3.UnimplementedExternalProcessorServer
}

func headerVal(h *corev3.HeaderValue) string {
	if len(h.RawValue) > 0 {
		return string(h.RawValue)
	}
	return h.Value
}

func (s *server) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {
	for {
		req, err := srv.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		var resp *extprocv3.ProcessingResponse
		switch v := req.Request.(type) {
		case *extprocv3.ProcessingRequest_RequestHeaders:
			trigger := false
			if v.RequestHeaders != nil && v.RequestHeaders.Headers != nil {
				for _, h := range v.RequestHeaders.Headers.GetHeaders() {
					if h.Key == "x-add-extproc" && headerVal(h) == "true" {
						trigger = true
					}
				}
			}
			set := []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "x-mutator-ran", RawValue: []byte("true")}},
			}
			if trigger {
				set = append(set, &corev3.HeaderValueOption{
					Header: &corev3.HeaderValue{Key: "add-new-extproc", RawValue: []byte("injected-by-mutator")},
				})
				log.Printf("mutator: x-add-extproc=true -> INJECTING add-new-extproc")
			} else {
				log.Printf("mutator: no trigger -> NOT injecting add-new-extproc")
			}
			resp = &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							HeaderMutation: &extprocv3.HeaderMutation{SetHeaders: set},
						},
					},
				},
			}
		case *extprocv3.ProcessingRequest_RequestBody:
			resp = &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{RequestBody: &extprocv3.BodyResponse{}},
			}
		case *extprocv3.ProcessingRequest_ResponseHeaders:
			resp = &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseHeaders{ResponseHeaders: &extprocv3.HeadersResponse{}},
			}
		default:
			log.Printf("mutator: unhandled request type %T", v)
			continue
		}
		if err := srv.Send(resp); err != nil {
			return err
		}
	}
}

func main() {
	var port int
	flag.IntVar(&port, "port", 9002, "gRPC port")
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	gs := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(gs, &server{})

	go func() {
		http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		_ = http.ListenAndServe(":8080", nil)
	}()

	log.Printf("ext-proc mutator listening on :%d (plaintext h2c)", port)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
