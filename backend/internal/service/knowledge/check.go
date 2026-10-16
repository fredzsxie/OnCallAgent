package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	localclient "github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type IndexReport struct {
	Field  string            `json:"field"`
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
	State  string            `json:"state"`
}

type Report struct {
	ServerVersion     string         `json:"server_version"`
	SDKVersion        string         `json:"sdk_version"`
	VersionCompatible bool           `json:"version_compatible"`
	Database          string         `json:"database"`
	Collection        string         `json:"collection"`
	Exists            bool           `json:"collection_exists"`
	Schema            *entity.Schema `json:"schema,omitempty"`
	Dimension         int            `json:"dimension"`
	Indexes           []IndexReport  `json:"indexes"`
	LoadState         string         `json:"load_state"`
	Differences       []string       `json:"differences"`
	Ready             bool           `json:"ready"`
}

func Check(ctx context.Context, db client.Client, cfg config.Milvus, dimension int) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	r := Report{SDKVersion: localclient.MilvusSDKVersion, Database: cfg.Database, Collection: cfg.Collection, Indexes: []IndexReport{}, Differences: []string{}, LoadState: "not_exist"}
	var err error
	if r.ServerVersion, err = db.GetVersion(ctx); err != nil {
		return r, operationError(ctx, "read version", err)
	}
	version := strings.TrimPrefix(r.ServerVersion, "v")
	r.VersionCompatible = strings.HasPrefix(version, "2.4.")
	if !r.VersionCompatible {
		r.Differences = append(r.Differences, "SDK v2.4.2 is validated for Milvus 2.4.x; server version differs")
	}
	if r.Exists, err = db.HasCollection(ctx, cfg.Collection); err != nil {
		return r, operationError(ctx, "check collection", err)
	}
	if !r.Exists {
		r.Differences = append(r.Differences, "collection is missing; run init_knowledge explicitly")
		return r, nil
	}
	coll, err := db.DescribeCollection(ctx, cfg.Collection)
	if err != nil {
		return r, operationError(ctx, "describe collection", err)
	}
	r.Schema = coll.Schema
	r.Dimension = VectorDimension(r.Schema)
	r.Differences = append(r.Differences, SchemaDiff(r.Schema, dimension)...)
	if r.Schema == nil {
		return r, errors.New("Milvus returned no collection schema")
	}
	vectorIndexFound := false
	for _, field := range r.Schema.Fields {
		indexes, err := db.DescribeIndex(ctx, cfg.Collection, field.Name)
		if err != nil {
			// SDK 2.4 does not expose a typed IndexNotFound error. Never suppress other failures.
			if strings.Contains(strings.ToLower(err.Error()), "index not found") {
				continue
			}
			return r, operationError(ctx, "describe index", err)
		}
		for _, index := range indexes {
			state, err := db.GetIndexState(ctx, cfg.Collection, field.Name, client.WithIndexName(index.Name()))
			if err != nil {
				return r, operationError(ctx, "read index state", err)
			}
			r.Indexes = append(r.Indexes, IndexReport{field.Name, index.Name(), index.Params(), commonpb.IndexState(state).String()})
			if field.Name == "vector" {
				vectorIndexFound = true
				if index.IndexType() != entity.HNSW || index.Params()["metric_type"] != string(entity.COSINE) {
					r.Differences = append(r.Differences, "vector index: expected HNSW with COSINE; existing index is preserved")
				}
				if commonpb.IndexState(state) != commonpb.IndexState_Finished {
					r.Differences = append(r.Differences, "vector index is not finished")
				}
			}
		}
	}
	if !vectorIndexFound {
		r.Differences = append(r.Differences, "vector index is missing")
	}
	loadState, err := db.GetLoadState(ctx, cfg.Collection, nil)
	if err != nil {
		return r, operationError(ctx, "read load state", err)
	}
	switch loadState {
	case entity.LoadStateLoaded:
		r.LoadState = "loaded"
	case entity.LoadStateLoading:
		r.LoadState = "loading"
	case entity.LoadStateNotLoad:
		r.LoadState = "not_loaded"
	default:
		r.LoadState = "not_exist"
	}
	r.Ready = len(r.Differences) == 0 && loadState == entity.LoadStateLoaded
	return r, nil
}

func Init(ctx context.Context, db client.Client, cfg config.Milvus, dimension int) (Report, error) {
	if dimension < 1 || dimension > 32768 {
		return Report{}, errors.New("invalid embedding dimension")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	r, err := Check(ctx, db, cfg, dimension)
	if err != nil {
		return r, err
	}
	if !r.VersionCompatible {
		return r, errors.New("Milvus server version is outside the selected SDK compatibility range")
	}
	if r.Exists {
		// Validate everything before any mutation. Never drop, replace or migrate existing data.
		if diffs := SchemaDiff(r.Schema, dimension); len(diffs) != 0 {
			return r, errors.New("existing collection schema differs; choose a new collection name")
		}
		for _, idx := range r.Indexes {
			if idx.Field == "vector" && (idx.Params["index_type"] != "HNSW" || idx.Params["metric_type"] != "COSINE") {
				return r, errors.New("existing vector index differs; choose a new collection name")
			}
		}
	} else {
		if err := db.CreateCollection(ctx, Schema(cfg.Collection, dimension), 1, client.WithConsistencyLevel(entity.ClStrong)); err != nil {
			return r, operationError(ctx, "create collection", err)
		}
	}
	hasIndex := false
	for _, idx := range r.Indexes {
		hasIndex = hasIndex || idx.Field == "vector"
	}
	if !hasIndex {
		idx, err := entity.NewIndexHNSW(entity.COSINE, 16, 128)
		if err != nil {
			return r, err
		}
		if err := db.CreateIndex(ctx, cfg.Collection, "vector", idx, false, client.WithIndexName("vector_cosine")); err != nil {
			return r, operationError(ctx, "create vector index", err)
		}
	}
	if err := db.LoadCollection(ctx, cfg.Collection, false); err != nil {
		return r, operationError(ctx, "load collection", err)
	}
	r, err = Check(ctx, db, cfg, dimension)
	if err == nil && !r.Ready {
		err = errors.New("collection is not ready after initialization")
	}
	return r, err
}

func operationError(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return context.Canceled
	}
	return fmt.Errorf("Milvus could not %s (RPC %s); check connectivity, permissions and server state", action, status.Code(err))
}
