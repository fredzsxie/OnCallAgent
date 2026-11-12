package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
)

func Init(ctx context.Context, db client.Milvus, emb embedding.Embedder, cfg config.Milvus) (dimensionResult int, err error) {
	return initCollection(ctx, db, emb, cfg, false)
}
func InitV2(ctx context.Context, db client.Milvus, emb embedding.Embedder, cfg config.Milvus) (int, error) {
	return initCollection(ctx, db, emb, cfg, true)
}
func initCollection(ctx context.Context, db client.Milvus, emb embedding.Embedder, cfg config.Milvus, v2 bool) (dimensionResult int, err error) {
	started := time.Now()
	defer func() {
		logger.Finish(ctx, "Milvus", "集合初始化", started, err, logger.F("dimension", dimensionResult))
	}()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	vectors, err := emb.EmbedStrings(ctx, []string{"OnCallAgent embedding dimension check"})
	if err != nil {
		return 0, err
	}
	if len(vectors) != 1 {
		return 0, errors.New("expected one embedding for dimension detection")
	}
	dimension := len(vectors[0])
	if _, err := client.FloatVector(vectors[0], dimension); err != nil {
		return 0, err
	}
	exists, err := db.HasCollection(ctx, cfg.Collection)
	if err != nil {
		return 0, operationError(ctx, "check collection", err)
	}
	wanted := Schema(cfg.Collection, dimension)
	if v2 {
		wanted = SchemaV2(cfg.Collection, dimension)
	}
	hasIndex := false
	if exists {
		collection, err := db.DescribeCollection(ctx, cfg.Collection)
		if err != nil {
			return 0, operationError(ctx, "describe collection", err)
		}
		// Check the existing schema and metric before making any changes.
		if diff := schemaDiff(collection.Schema, wanted); len(diff) != 0 {
			return 0, fmt.Errorf("existing collection differs: %s", strings.Join(diff, "; "))
		}
		if hasIndex, err = vectorIndex(ctx, db, cfg.Collection); err != nil {
			return 0, err
		}
	} else if err := db.CreateCollection(ctx, wanted); err != nil {
		return 0, operationError(ctx, "create collection", err)
	}
	if !hasIndex {
		idx := index.NewHNSWIndex(entity.COSINE, 16, 128)
		if err := db.CreateIndex(ctx, cfg.Collection, "vector", idx); err != nil {
			return 0, operationError(ctx, "create vector index", err)
		}
	}
	if v2 {
		exists, err := checkIndex(ctx, db, cfg.Collection, "sparse_vector", "SPARSE_INVERTED_INDEX", "BM25")
		if err != nil {
			return 0, err
		}
		if !exists {
			if err = db.CreateIndex(ctx, cfg.Collection, "sparse_vector", index.NewSparseInvertedIndex(entity.BM25, 0)); err != nil {
				return 0, operationError(ctx, "create BM25 index", err)
			}
		}
	}
	if err := db.LoadCollection(ctx, cfg.Collection); err != nil {
		return 0, operationError(ctx, "load collection", err)
	}
	return dimension, nil
}

func vectorIndex(ctx context.Context, db client.Milvus, collection string) (bool, error) {
	return checkIndex(ctx, db, collection, "vector", "HNSW", "COSINE")
}
func checkIndex(ctx context.Context, db client.Milvus, collection, field, kind, metric string) (bool, error) {
	indexes, err := db.DescribeIndex(ctx, collection, field)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "index not found") {
			return false, nil
		}
		return false, operationError(ctx, "describe index", err)
	}
	for _, def := range indexes {
		if def.Params()["index_type"] != kind || def.Params()["metric_type"] != metric {
			return false, fmt.Errorf("existing %s index differs: expected %s/%s", field, kind, metric)
		}
	}
	return len(indexes) > 0, nil
}

func operationError(ctx context.Context, action string, causes ...error) error {
	if len(causes) > 0 {
		logger.Failure(ctx, "Milvus", "数据库操作失败", causes[0], logger.F("operation", action))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("Milvus could not %s", action)
}
