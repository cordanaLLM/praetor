package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EndpointError is one local runtime endpoint that did not return a model list.
type EndpointError struct {
	Endpoint string
	Err      error
}

func (e *EndpointError) Error() string {
	return fmt.Sprintf("local model endpoint %s: %v", e.Endpoint, e.Err)
}

func (e *EndpointError) Unwrap() error { return e.Err }

// errListingNotServed means an endpoint answered 404 for one listing path, so the next
// protocol is tried.
var errListingNotServed = errors.New("model listing path not served")

// listingPaths is tried in order. Ollama serves its native /api/tags; vLLM, and any
// other OpenAI-compatible server, serves /v1/models and answers 404 for /api/tags. Ollama
// also serves /v1/models, so the native listing goes first and keeps its model names.
var listingPaths = []string{"/api/tags", "/v1/models"}

// listingPayload decodes both listing shapes; a response carries only its own field.
type listingPayload struct {
	// Models is the Ollama /api/tags shape.
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	// Data is the OpenAI /v1/models shape.
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func decodeListing(body []byte) ([]string, error) {
	var payload listingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Models)+len(payload.Data) > MaxRoutingModels {
		return nil, errors.New("local catalog exceeds model limit")
	}
	ids := make([]string, 0, len(payload.Models)+len(payload.Data))
	for _, model := range payload.Models {
		ids = append(ids, model.Name)
	}
	for _, model := range payload.Data {
		ids = append(ids, model.ID)
	}
	return ids, nil
}

// fetchListing reads one bounded listing body. A 404 is errListingNotServed.
func fetchListing(ctx context.Context, client *http.Client, url string) (body []byte, resultErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, resp.Body.Close()) }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errListingNotServed
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local catalog returned HTTP %d", resp.StatusCode)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, MaxRoutingFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxRoutingFileBytes {
		return nil, errors.New("local catalog exceeds byte limit")
	}
	return body, nil
}

// queryLocalEndpoint lists one endpoint's models through the first protocol it serves.
func queryLocalEndpoint(ctx context.Context, client *http.Client, ep string) ([]ModelDescriptor, error) {
	for _, path := range listingPaths {
		body, err := fetchListing(ctx, client, ep+path)
		if errors.Is(err, errListingNotServed) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ids, err := decodeListing(body)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		return localDescriptors(ids), nil
	}
	return nil, errors.New("serves neither /api/tags nor /v1/models")
}

// localDescriptors builds free local entries whose family is the lineage their name shows.
func localDescriptors(ids []string) []ModelDescriptor {
	models := make([]ModelDescriptor, 0, len(ids))
	for _, id := range ids {
		models = append(models, ModelDescriptor{
			ID: id, Family: DetectFamily(id),
			RPMLimit: 50000, TPMLimit: 20000000,
		})
	}
	return models
}

// DiscoverLocalModels lists the models installed on every local Ollama or vLLM endpoint.
// An endpoint that fails does not stop the others: the models of every endpoint that
// answered are returned together with one joined *EndpointError per failed endpoint,
// so a caller can keep the partial inventory and still report what it could not reach.
func DiscoverLocalModels(ctx context.Context, endpoints []string) ([]ModelDescriptor, error) {
	discovered := make([]ModelDescriptor, 0)
	client := &http.Client{Timeout: 3 * time.Second}
	var failures []error
	for _, ep := range endpoints {
		models, err := queryLocalEndpoint(ctx, client, ep)
		if err != nil {
			failures = append(failures, &EndpointError{Endpoint: ep, Err: err})
			continue
		}
		discovered = append(discovered, models...)
	}
	return discovered, errors.Join(failures...)
}
