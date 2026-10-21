package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	localclient "github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

func Init(ctx context.Context, db client.Client, emb embedding.Embedder, cfg config.Milvus) (dimensionResult int, err error) {
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
	if _, err := localclient.FloatVector(vectors[0], dimension); err != nil {
		return 0, err
	}
	exists, err := db.HasCollection(ctx, cfg.Collection)
	if err != nil {
		return 0, operationError(ctx, "check collection", err)
	}
	hasIndex := false
	if exists {
		collection, err := db.DescribeCollection(ctx, cfg.Collection)
		if err != nil {
			return 0, operationError(ctx, "describe collection", err)
		}
		// Check the existing schema and metric before making any changes.
		if diff := SchemaDiff(collection.Schema, dimension); len(diff) != 0 {
			return 0, fmt.Errorf("existing collection differs: %s", strings.Join(diff, "; "))
		}
		if hasIndex, err = vectorIndex(ctx, db, cfg.Collection); err != nil {
			return 0, err
		}
	} else if err := db.CreateCollection(ctx, Schema(cfg.Collection, dimension), 1, client.WithConsistencyLevel(entity.ClStrong)); err != nil {
		return 0, operationError(ctx, "create collection", err)
	}
	if !hasIndex {
		index, err := entity.NewIndexHNSW(entity.COSINE, 16, 128)
		if err != nil {
			return 0, err
		}
		if err := db.CreateIndex(ctx, cfg.Collection, "vector", index, false, client.WithIndexName("vector_cosine")); err != nil {
			return 0, operationError(ctx, "create vector index", err)
		}
	}
	if err := db.LoadCollection(ctx, cfg.Collection, false); err != nil {
		return 0, operationError(ctx, "load collection", err)
	}
	return dimension, nil
}

func vectorIndex(ctx context.Context, db client.Client, collection string) (bool, error) {
	indexes, err := db.DescribeIndex(ctx, collection, "vector")
	if err != nil {
		// SDK 2.4 returns an untyped error when the vector index does not exist.
		if strings.Contains(strings.ToLower(err.Error()), "index not found") {
			return false, nil
		}
		return false, operationError(ctx, "describe vector index", err)
	}
	for _, index := range indexes {
		if index.IndexType() != entity.HNSW || index.Params()["metric_type"] != string(entity.COSINE) {
			return false, fmt.Errorf("existing vector index differs: expected HNSW/COSINE, got %s/%s", index.IndexType(), index.Params()["metric_type"])
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
