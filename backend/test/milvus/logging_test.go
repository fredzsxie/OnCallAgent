package milvus_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type queryLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *queryLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}
func (b *queryLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type queryLogServer struct {
	server
	requests chan any
	failure  codes.Code
}

func (s *queryLogServer) DescribeCollection(_ context.Context, request *milvuspb.DescribeCollectionRequest) (*milvuspb.DescribeCollectionResponse, error) {
	return &milvuspb.DescribeCollectionResponse{Status: &commonpb.Status{}, CollectionID: 1, Schema: knowledge.SchemaV2(request.CollectionName, 3).ProtoMessage(), ConsistencyLevel: commonpb.ConsistencyLevel_Strong}, nil
}
func (s *queryLogServer) Search(_ context.Context, request *milvuspb.SearchRequest) (*milvuspb.SearchResults, error) {
	s.requests <- request
	if s.failure != codes.OK {
		return nil, status.Error(s.failure, "private-milvus-provider-error")
	}
	return queryLogResults(int(request.Nq)), nil
}
func (s *queryLogServer) HybridSearch(_ context.Context, request *milvuspb.HybridSearchRequest) (*milvuspb.SearchResults, error) {
	s.requests <- request
	if s.failure != codes.OK {
		return nil, status.Error(s.failure, "private-milvus-provider-error")
	}
	return queryLogResults(1), nil
}
func (s *queryLogServer) Query(_ context.Context, request *milvuspb.QueryRequest) (*milvuspb.QueryResults, error) {
	s.requests <- request
	if s.failure != codes.OK {
		return nil, status.Error(s.failure, "private-milvus-provider-error")
	}
	return &milvuspb.QueryResults{Status: &commonpb.Status{}, OutputFields: []string{"content"}, FieldsData: []*schemapb.FieldData{column.NewColumnVarChar("content", []string{"private-stored-content", "private-stored-content"}).FieldData()}}, nil
}

func queryLogResults(queries int) *milvuspb.SearchResults {
	data := &schemapb.SearchResultData{NumQueries: int64(queries), TopK: 1}
	var ids, contents []string
	var metadata [][]byte
	for i := 0; i < queries; i++ {
		data.Topks = append(data.Topks, 1)
		data.Scores = append(data.Scores, .9)
		ids = append(ids, fmt.Sprintf("chunk-%d", i))
		contents = append(contents, "private-stored-content")
		metadata = append(metadata, []byte(`{"source":"private-stored-source"}`))
	}
	data.Ids = &schemapb.IDs{IdField: &schemapb.IDs_StrId{StrId: &schemapb.StringArray{Data: ids}}}
	data.FieldsData = []*schemapb.FieldData{column.NewColumnVarChar("content", contents).FieldData(), column.NewColumnJSONBytes("metadata", metadata).FieldData()}
	return &milvuspb.SearchResults{Status: &commonpb.Status{}, Results: data}
}

func loggedMilvus(t *testing.T, failure codes.Code) (context.Context, client.Milvus, *queryLogBuffer, *queryLogServer) {
	t.Helper()
	output := &queryLogBuffer{}
	logs, err := logger.New(config.Logging{Level: "info", Dir: t.TempDir(), RetentionDays: 7}, logger.Options{Console: output})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close() })
	ctx, _ := logger.WithRequest(logger.WithLogger(context.Background(), logs), "milvus-query-1")
	fixture := &queryLogServer{requests: make(chan any, 16), failure: failure}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpc := grpc.NewServer()
	milvuspb.RegisterMilvusServiceServer(rpc, fixture)
	go rpc.Serve(listener)
	t.Cleanup(rpc.Stop)
	db, err := client.NewMilvus(ctx, config.Milvus{Address: listener.Addr().String(), Database: "logging_db", Username: "fixture-user", Password: "private-db-password", ConnectTimeout: time.Second, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db, output, fixture
}

func queryLogParams(pairs []*commonpb.KeyValuePair) map[string]string {
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		result[pair.Key] = pair.Value
	}
	return result
}

func TestMilvusQueryLogsActualSDKParameters(t *testing.T) {
	for _, operation := range []string{"Search", "SearchNilParams", "BM25", "HybridSearch", "Query"} {
		t.Run(operation, func(t *testing.T) {
			ctx, db, output, fixture := loggedMilvus(t, codes.OK)
			const collection, filter, text = "knowledge_v2", `document_id == "doc-a"`, "inspect cache errors"
			vector := entity.FloatVector{.1234567, .2345678, .3456789}
			count := 2
			want := []string{"filter[" + filter + "]", `output_fields[["content"`, "rid[milvus-query-1]", "collection[knowledge_v2]", "consistency[Strong]", "elapsed["}
			switch operation {
			case "Search", "SearchNilParams", "BM25":
				vectors, field, metric := []entity.Vector{vector, vector}, "vector", entity.COSINE
				var params index.AnnParam = index.NewHNSWAnnParam(128)
				if operation == "BM25" {
					vectors, field, metric, params = []entity.Vector{entity.Text(text)}, "sparse_vector", entity.BM25, index.NewSparseAnnParam()
					count = 1
					want = append(want, `queries[["inspect cache errors"`)
				} else if operation == "SearchNilParams" {
					params = nil
					want = append(want, "dimension[3]", "ann_params[{}]")
				} else {
					want = append(want, "dimension[3]", `ann_params[{"ef":128}]`)
				}
				results, err := db.Search(ctx, collection, filter, []string{"content", "metadata"}, vectors, field, metric, 5, params)
				if err != nil || len(results) != count {
					t.Fatal("SDK result changed", len(results), err)
				}
				request := (<-fixture.requests).(*milvuspb.SearchRequest)
				search := queryLogParams(request.SearchParams)
				if request.CollectionName != collection || request.Dsl != filter || !reflect.DeepEqual(request.OutputFields, []string{"content", "metadata"}) || request.UseDefaultConsistency || request.ConsistencyLevel != commonpb.ConsistencyLevel_Strong || search["anns_field"] != field || search["metric_type"] != string(metric) || search["topk"] != "5" || search["offset"] != "0" {
					t.Fatal("search options changed", request)
				}
				if operation == "Search" && search["params"] != `{"ef":128}` || operation == "SearchNilParams" && search["params"] != `{}` {
					t.Fatal("ANN parameters changed", search["params"])
				}
				var placeholders commonpb.PlaceholderGroup
				if proto.Unmarshal(request.PlaceholderGroup, &placeholders) != nil || len(placeholders.Placeholders) != 1 || len(placeholders.Placeholders[0].Values) != count || !bytes.Equal(placeholders.Placeholders[0].Values[0], vectors[0].Serialize()) {
					t.Fatal("search vectors changed")
				}
				want = append(want, "operation[Search]", "anns_field["+field+"]", "metric["+string(metric)+"]", "topk[5]", "offset[0]", fmt.Sprintf("vector_count[%d]", count))
			case "HybridSearch":
				results, err := db.(client.HybridMilvus).HybridSearch(ctx, collection, vector, text, filter, 8, 3)
				if err != nil || len(results) != 1 || results[0].ResultCount != 1 {
					t.Fatal("hybrid result changed", results, err)
				}
				request := (<-fixture.requests).(*milvuspb.HybridSearchRequest)
				rank := queryLogParams(request.RankParams)
				if request.CollectionName != collection || len(request.Requests) != 2 || !reflect.DeepEqual(request.OutputFields, []string{"content", "metadata"}) || request.UseDefaultConsistency || request.ConsistencyLevel != commonpb.ConsistencyLevel_Strong || rank["strategy"] != "rrf" || rank["params"] != `{"k":60}` || rank["limit"] != "3" {
					t.Fatal("hybrid options changed", request)
				}
				for i, request := range request.Requests {
					params := queryLogParams(request.SearchParams)
					if request.Dsl != filter || params["topk"] != "8" || params["metric_type"] != []string{"COSINE", "BM25"}[i] || params["anns_field"] != []string{"vector", "sparse_vector"}[i] {
						t.Fatal("hybrid ANN options changed", request)
					}
				}
				count = 1
				want = append(want, "operation[HybridSearch]", "query["+text+"]", "dense_field[vector]", "dense_metric[COSINE]", "sparse_field[sparse_vector]", "sparse_metric[BM25]", "topk[8]", "limit[3]", "rrf_k[60]", "ef[64]", "dimension[3]")
			case "Query":
				results, err := db.Query(ctx, collection, filter, []string{"content"}, 7, 2)
				if err != nil || results.Len() != 2 {
					t.Fatal("query result changed", results, err)
				}
				request := (<-fixture.requests).(*milvuspb.QueryRequest)
				params := queryLogParams(request.QueryParams)
				if request.CollectionName != collection || request.Expr != filter || !reflect.DeepEqual(request.OutputFields, []string{"content"}) || request.UseDefaultConsistency || request.ConsistencyLevel != commonpb.ConsistencyLevel_Strong || params["limit"] != "7" || params["offset"] != "2" {
					t.Fatal("query options changed", request)
				}
				want = append(want, "operation[Query]", "limit[7]", "offset[2]")
			}
			logs := output.String()
			for _, value := range append(want, fmt.Sprintf("count[%d]", count)) {
				if !strings.Contains(logs, value) {
					t.Fatalf("missing %q: %s", value, logs)
				}
			}
			if strings.Count(logs, "发起查询") != 1 || strings.Count(logs, "查询完成") != 1 || len(fixture.requests) != 0 {
				t.Fatal("query logging duplicated SDK calls or records", logs)
			}
			for _, secret := range []string{"private-stored-content", "private-stored-source", "private-db-password", "0.1234567", "0.2345678", "0.3456789"} {
				if strings.Contains(logs, secret) {
					t.Fatal("stored content, credentials or vector values leaked", secret)
				}
			}
		})
	}
}

func TestMilvusQueryLogBoundsAndSafeErrors(t *testing.T) {
	for _, operation := range []string{"Search", "HybridSearch", "Query"} {
		for _, failure := range []codes.Code{codes.OK, codes.PermissionDenied, codes.DeadlineExceeded} {
			t.Run(operation+"/"+failure.String(), func(t *testing.T) {
				ctx, db, output, fixture := loggedMilvus(t, failure)
				query := strings.Repeat("private-long-query", logger.PreviewLimit)
				vector := entity.FloatVector{.1234567, .2345678, .3456789}
				var err error
				switch operation {
				case "Search":
					params := index.NewSparseAnnParam()
					params.WithExtraParam("api_key", "private-ann-secret")
					_, err = db.Search(ctx, "knowledge_v2", query, []string{"content", "metadata"}, []entity.Vector{entity.Text(query)}, "sparse_vector", entity.BM25, 5, params)
				case "HybridSearch":
					_, err = db.(client.HybridMilvus).HybridSearch(ctx, "knowledge_v2", vector, query, query, 8, 3)
				case "Query":
					_, err = db.Query(ctx, "knowledge_v2", query, []string{"content"}, 7, 2)
				}
				if (err != nil) != (failure != codes.OK) {
					t.Fatal("query outcome changed", err)
				}
				request := <-fixture.requests
				switch request := request.(type) {
				case *milvuspb.SearchRequest:
					params := queryLogParams(request.SearchParams)
					var ann map[string]any
					if request.Dsl != query || json.Unmarshal([]byte(params["params"]), &ann) != nil || ann["api_key"] != "private-ann-secret" {
						t.Fatal("preview changed actual search parameters")
					}
				case *milvuspb.HybridSearchRequest:
					var placeholders commonpb.PlaceholderGroup
					if len(request.Requests) != 2 || request.Requests[0].Dsl != query || proto.Unmarshal(request.Requests[1].PlaceholderGroup, &placeholders) != nil || string(placeholders.Placeholders[0].Values[0]) != query {
						t.Fatal("preview truncated actual hybrid query")
					}
				case *milvuspb.QueryRequest:
					if request.Expr != query {
						t.Fatal("preview truncated actual query")
					}
				}
				logs := output.String()
				if !strings.Contains(logs, "filter[<truncated>]") || strings.Contains(logs, "private-") || len(logs) > logger.PreviewLimit {
					t.Fatal("query log was unbounded or exposed private data", logs)
				}
				if operation == "Search" && !strings.Contains(logs, "REDACTED") {
					t.Fatal("ANN parameter credentials were not redacted", logs)
				}
				if failure == codes.OK {
					if !strings.Contains(logs, "查询完成") {
						t.Fatal("missing query completion", logs)
					}
				} else {
					category := map[codes.Code]string{codes.PermissionDenied: "authentication_failed", codes.DeadlineExceeded: "timeout"}[failure]
					if strings.Contains(logs, "查询完成") || !strings.Contains(logs, "error_type["+category+"]") || !strings.Contains(logs, "count[0]") {
						t.Fatal("query error was not recorded safely", logs)
					}
				}
			})
		}
	}
}
