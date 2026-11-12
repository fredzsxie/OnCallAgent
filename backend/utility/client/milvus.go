package client

import (
	"context"
	"errors"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// Milvus keeps SDK lifecycle and consistency choices outside business services.
type Milvus interface {
	Close() error
	GetVersion(context.Context) (string, error)
	HasCollection(context.Context, string) (bool, error)
	DescribeCollection(context.Context, string) (*entity.Collection, error)
	CreateCollection(context.Context, *entity.Schema) error
	DescribeIndex(context.Context, string, string) ([]index.Index, error)
	CreateIndex(context.Context, string, string, index.Index) error
	LoadCollection(context.Context, string) error
	Upsert(context.Context, string, ...column.Column) (column.Column, error)
	Delete(context.Context, string, string) error
	Search(context.Context, string, string, []string, []entity.Vector, string, entity.MetricType, int, index.AnnParam) ([]milvusclient.ResultSet, error)
	Query(context.Context, string, string, []string, int, int) (milvusclient.DataSet, error)
}

type milvusDB struct {
	sdk *milvusclient.Client
	ctx context.Context
}

func NewMilvus(ctx context.Context, cfg config.Milvus) (out Milvus, err error) {
	root := ctx
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	connection, cause := milvusclient.New(ctx, &milvusclient.ClientConfig{
		Address: cfg.Address, DBName: cfg.Database, Username: cfg.Username, Password: cfg.Password, EnableTLSAuth: cfg.TLS,
	})
	if cause != nil {
		logger.Finish(root, "Milvus", "连接初始化", started, cause)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Milvus connection failed; check endpoint, database, authentication and TLS")
	}
	logger.Finish(root, "Milvus", "连接初始化", started, nil)
	return &milvusDB{sdk: connection, ctx: root}, nil
}
func (c *milvusDB) Close() error {
	// Shutdown must also close a connection after the application context is cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.sdk.Close(ctx)
	logger.Finish(c.ctx, "Milvus", "连接关闭", time.Now(), err)
	return err
}
func (c *milvusDB) GetVersion(ctx context.Context) (string, error) {
	return c.sdk.GetServerVersion(ctx, milvusclient.NewGetServerVersionOption())
}
func (c *milvusDB) HasCollection(ctx context.Context, name string) (bool, error) {
	return c.sdk.HasCollection(ctx, milvusclient.NewHasCollectionOption(name))
}
func (c *milvusDB) DescribeCollection(ctx context.Context, name string) (*entity.Collection, error) {
	return c.sdk.DescribeCollection(ctx, milvusclient.NewDescribeCollectionOption(name))
}
func (c *milvusDB) CreateCollection(ctx context.Context, schema *entity.Schema) error {
	return c.sdk.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(schema.CollectionName, schema).WithConsistencyLevel(entity.ClStrong).WithShardNum(1))
}
func (c *milvusDB) DescribeIndex(ctx context.Context, collection, field string) ([]index.Index, error) {
	names, err := c.sdk.ListIndexes(ctx, milvusclient.NewListIndexOption(collection).WithFieldName(field))
	if err != nil {
		return nil, err
	}
	result := make([]index.Index, 0, len(names))
	for _, name := range names {
		d, err := c.sdk.DescribeIndex(ctx, milvusclient.NewDescribeIndexOption(collection, name))
		if err != nil {
			return nil, err
		}
		result = append(result, index.NewGenericIndex(name, d.Params()))
	}
	return result, nil
}
func (c *milvusDB) CreateIndex(ctx context.Context, collection, field string, def index.Index) error {
	task, err := c.sdk.CreateIndex(ctx, milvusclient.NewCreateIndexOption(collection, field, def).WithIndexName(field+"_index"))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}
func (c *milvusDB) LoadCollection(ctx context.Context, collection string) error {
	task, err := c.sdk.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collection))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}
func (c *milvusDB) Upsert(ctx context.Context, collection string, cols ...column.Column) (column.Column, error) {
	r, err := c.sdk.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(collection).WithColumns(cols...))
	return r.IDs, err
}
func (c *milvusDB) Delete(ctx context.Context, collection, filter string) error {
	_, err := c.sdk.Delete(ctx, milvusclient.NewDeleteOption(collection).WithExpr(filter))
	return err
}
func (c *milvusDB) Search(ctx context.Context, collection, filter string, fields []string, vectors []entity.Vector, field string, metric entity.MetricType, k int, params index.AnnParam) ([]milvusclient.ResultSet, error) {
	// Strong reads are required before publishing a newly indexed document version.
	return c.sdk.Search(ctx, milvusclient.NewSearchOption(collection, k, vectors).WithANNSField(field).WithFilter(filter).WithOutputFields(fields...).WithAnnParam(params).WithSearchParam("metric_type", string(metric)).WithConsistencyLevel(entity.ClStrong))
}
func (c *milvusDB) Query(ctx context.Context, collection, filter string, fields []string, limit, offset int) (milvusclient.DataSet, error) {
	r, err := c.sdk.Query(ctx, milvusclient.NewQueryOption(collection).WithFilter(filter).WithOutputFields(fields...).WithLimit(limit).WithOffset(offset).WithConsistencyLevel(entity.ClStrong))
	return r.Fields, err
}

func Column(data milvusclient.DataSet, name string) column.Column {
	for _, c := range data {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func JSONAt(c column.Column, i int) ([]byte, error) {
	value, err := c.Get(i)
	if err != nil {
		return nil, err
	}
	raw, ok := value.([]byte)
	if !ok {
		return nil, errors.New("invalid JSON column value")
	}
	return raw, nil
}
