package catalog

import (
	"reflect"
	"testing"
)

func TestPostgreSQLTrackEffectiveColumns(t *testing.T) {
	fixture := newPlacementFixture(t)
	type column struct {
		position         int
		name, dataType   string
		underlyingSchema string
		underlyingType   string
	}
	columns := func(relation string) []column {
		t.Helper()
		rows, err := fixture.database.Query(`SELECT ordinal_position,column_name,data_type,udt_schema,udt_name
			FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, relation)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []column
		for rows.Next() {
			var item column
			if err := rows.Scan(&item.position, &item.name, &item.dataType, &item.underlyingSchema, &item.underlyingType); err != nil {
				t.Fatal(err)
			}
			result = append(result, item)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(result) == 0 {
			t.Fatalf("%s 没有列", relation)
		}
		return result
	}
	if want, got := columns("tracks"), columns("track_effective"); !reflect.DeepEqual(want, got) {
		t.Fatalf("track_effective 的列与 tracks 不一致\ntrack_effective: %+v\ntracks: %+v", got, want)
	}
}
