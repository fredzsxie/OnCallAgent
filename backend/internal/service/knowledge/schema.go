package knowledge

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const IDMaxLength = 256
const ContentMaxLength = 65535

func Schema(collection string, dimension int) *entity.Schema {
	return entity.NewSchema().WithName(collection).WithAutoID(false).WithDynamicFieldEnabled(false).
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithIsPrimaryKey(true).WithMaxLength(IDMaxLength)).
		WithField(entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(ContentMaxLength)).
		WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dimension)))
}

func SchemaDiff(actual *entity.Schema, dimension int) []string {
	diffs := []string{}
	if actual == nil {
		return []string{"collection schema is missing"}
	}
	if actual.AutoID {
		diffs = append(diffs, "auto_id: expected false, got true")
	}
	if actual.EnableDynamicField {
		diffs = append(diffs, "dynamic fields: expected false, got true")
	}
	wanted := Schema(actual.CollectionName, dimension)
	fields := map[string]*entity.Field{}
	for _, f := range actual.Fields {
		fields[f.Name] = f
	}
	for _, expected := range wanted.Fields {
		f, ok := fields[expected.Name]
		if !ok {
			diffs = append(diffs, expected.Name+": missing field")
			continue
		}
		delete(fields, expected.Name)
		if f.DataType != expected.DataType {
			diffs = append(diffs, fmt.Sprintf("%s.type: expected %s, got %s", f.Name, expected.DataType.Name(), f.DataType.Name()))
		}
		if f.PrimaryKey != expected.PrimaryKey || f.AutoID {
			diffs = append(diffs, f.Name+": primary key / auto_id mismatch")
		}
		if f.IsPartitionKey || f.IsDynamic || f.IsClusteringKey {
			diffs = append(diffs, f.Name+": unexpected partition, dynamic or clustering field")
		}
		for key, value := range expected.TypeParams {
			if f.TypeParams[key] != value {
				diffs = append(diffs, fmt.Sprintf("%s.%s: expected %s, got %s", f.Name, key, value, f.TypeParams[key]))
			}
		}
	}
	for name := range fields {
		diffs = append(diffs, name+": unexpected field")
	}
	sort.Strings(diffs)
	return diffs
}

func VectorDimension(s *entity.Schema) int {
	if s != nil {
		for _, f := range s.Fields {
			if f.Name == "vector" && f.DataType == entity.FieldTypeFloatVector {
				dim, _ := strconv.Atoi(f.TypeParams["dim"])
				return dim
			}
		}
	}
	return 0
}
