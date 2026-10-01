package main

import (
	"encoding/json"
	"testing"
)

// The shape Docker 29 (API 1.56) returns for /system/df?type=build-cache&type=image.
const dfJSON = `{"LayersSize":2030813589,"ImageUsage":{"TotalSize":2030813589},
"BuildCache":[{"Type":"regular","Size":306601,"InUse":false,"Shared":true},
{"Type":"frontend","Size":43113664,"InUse":false,"Shared":false},
{"Type":"regular","Size":11088786,"InUse":false,"Shared":true},
{"Type":"regular","Size":5000,"InUse":true,"Shared":false}]}`

func TestDiskUsage(t *testing.T) {
	var du DiskUsage
	if err := json.Unmarshal([]byte(dfJSON), &du); err != nil {
		t.Fatal(err)
	}
	if got := du.ReclaimableCache(); got != 43113664 {
		t.Errorf("reclaimable cache: shared layers and entries in use must not count, got %d", got)
	}
	if got := du.ImagesSize(); got != 2030813589 {
		t.Errorf("images size %d", got)
	}
	var newer DiskUsage
	_ = json.Unmarshal([]byte(`{"ImageUsage":{"TotalSize":42}}`), &newer)
	if got := newer.ImagesSize(); got != 42 {
		t.Errorf("without LayersSize the size must come from ImageUsage, got %d", got)
	}
}
