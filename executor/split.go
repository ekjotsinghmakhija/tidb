// Copyright 2025 Ekjot Singh
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/cznic/mathutil"
	"github.com/pingcap/errors"
	"github.com/pingcap/failpoint"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/pingcap/parser/model"
	"github.com/pingcap/parser/mysql"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/store/tikv"
	"github.com/pingcap/tidb/table/tables"
	"github.com/pingcap/tidb/tablecodec"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/codec"
	"github.com/pingcap/tidb/util/logutil"
	"go.uber.org/zap"
)

// SplitIndexRegionExec represents a split index regions executor.
type SplitIndexRegionExec struct {
	baseExecutor

	tableInfo  *model.TableInfo
	indexInfo  *model.IndexInfo
	lower      []types.Datum
	upper      []types.Datum
	num        int
	valueLists [][]types.Datum
}

// Next implements the Executor Next interface.
func (e *SplitIndexRegionExec) Next(ctx context.Context, _ *chunk.Chunk) error {
	store := e.ctx.GetStore()
	s, ok := store.(kv.SplitableStore)
	if !ok {
		return nil
	}
	splitIdxKeys, err := e.getSplitIdxKeys()
	if err != nil {
		return err
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, e.ctx.GetSessionVars().GetSplitRegionTimeout())
	defer cancel()
	regionIDs := make([]uint64, 0, len(splitIdxKeys))
	for _, idxKey := range splitIdxKeys {
		regionID, err := s.SplitRegion(idxKey, true)
		if err != nil {
			logutil.Logger(context.Background()).Warn("split table index region failed",
				zap.String("table", e.tableInfo.Name.L),
				zap.String("index", e.indexInfo.Name.L),
				zap.Error(err))
			continue
		}
		regionIDs = append(regionIDs, regionID)

		if isCtxDone(ctxWithTimeout) {
			return errors.Errorf("wait split region timeout(%v)", e.ctx.GetSessionVars().GetSplitRegionTimeout())
		}
	}
	if !e.ctx.GetSessionVars().WaitSplitRegionFinish {
		return nil
	}
	for _, regionID := range regionIDs {
		err := s.WaitScatterRegionFinish(regionID)
		if err != nil {
			logutil.Logger(context.Background()).Warn("wait scatter region failed",
				zap.Uint64("regionID", regionID),
				zap.String("table", e.tableInfo.Name.L),
				zap.String("index", e.indexInfo.Name.L),
				zap.Error(err))
		}
		if isCtxDone(ctxWithTimeout) {
			return errors.Errorf("wait split region timeout(%v)", e.ctx.GetSessionVars().GetSplitRegionTimeout())
		}
	}
	return nil
}

func (e *SplitIndexRegionExec) getSplitIdxKeys() ([][]byte, error) {
	var idxKeys [][]byte
	if e.num > 0 {
		idxKeys = make([][]byte, 0, e.num)
	} else {
		idxKeys = make([][]byte, 0, len(e.valueLists)+1)
	}
	// Split in the start of the index key.
	startIdxKey := tablecodec.EncodeTableIndexPrefix(e.tableInfo.ID, e.indexInfo.ID)
	idxKeys = append(idxKeys, startIdxKey)

	index := tables.NewIndex(e.tableInfo.ID, e.tableInfo, e.indexInfo)
	// Split index regions by user specified value lists.
	if len(e.valueLists) > 0 {
		for _, v := range e.valueLists {
			idxKey, _, err := index.GenIndexKey(e.ctx.GetSessionVars().StmtCtx, v, math.MinInt64, nil)
			if err != nil {
				return nil, err
			}
			idxKeys = append(idxKeys, idxKey)
		}
		return idxKeys, nil
	}
	// Split index regions by lower, upper value and calculate the step by (upper - lower)/num.
	lowerIdxKey, _, err := index.GenIndexKey(e.ctx.GetSessionVars().StmtCtx, e.lower, math.MinInt64, nil)
	if err != nil {
		return nil, err
	}
	// Use math.MinInt64 as handle_id for the upper index key to avoid affecting calculate split point.
	// If use math.MaxInt64 here, test of `TestSplitIndex` will report error.
	upperIdxKey, _, err := index.GenIndexKey(e.ctx.GetSessionVars().StmtCtx, e.upper, math.MinInt64, nil)
	if err != nil {
		return nil, err
	}
	if bytes.Compare(lowerIdxKey, upperIdxKey) >= 0 {
		lowerStr, err1 := datumSliceToString(e.lower)
		upperStr, err2 := datumSliceToString(e.upper)
		if err1 != nil || err2 != nil {
			return nil, errors.Errorf("Split index `%v` region lower value %v should less than the upper value %v", e.indexInfo.Name, e.lower, e.upper)
		}
		return nil, errors.Errorf("Split index `%v` region lower value %v should less than the upper value %v", e.indexInfo.Name, lowerStr, upperStr)
	}
	return getValuesList(lowerIdxKey, upperIdxKey, e.num, idxKeys), nil
}

// getValuesList is used to get `num` values between lower and upper value.
// To Simplify the explain, suppose lower and upper value type is int64, and lower=0, upper=100, num=10,
// then calculate the step=(upper-lower)/num=10, then the function should return 0+10, 10+10, 20+10... all together 9 (num-1) values.
// Then the function will return [10,20,30,40,50,60,70,80,90].
// The difference is the value type of upper,lower is []byte, So I use getUint64FromBytes to convert []byte to uint64.
func getValuesList(lower, upper []byte, num int, valuesList [][]byte) [][]byte {
	commonPrefixIdx := longestCommonPrefixLen(lower, upper)
	step := getStepValue(lower[commonPrefixIdx:], upper[commonPrefixIdx:], num)
	startV := getUint64FromBytes(lower[commonPrefixIdx:], 0)
	// To get `num` regions, only need to split `num-1` idx keys.
	buf := make([]byte, 8)
	for i := 0; i < num-1; i++ {
		value := make([]byte, 0, commonPrefixIdx+8)
		value = append(value, lower[:commonPrefixIdx]...)
		startV += step
		binary.BigEndian.PutUint64(buf, startV)
		value = append(value, buf...)
		valuesList = append(valuesList, value)
	}
	return valuesList
}

// longestCommonPrefixLen gets the longest common prefix byte length.
func longestCommonPrefixLen(s1, s2 []byte) int {
	l := mathutil.Min(len(s1), len(s2))
	i := 0
	for ; i < l; i++ {
		if s1[i] != s2[i] {
			break
		}
	}
	return i
}

// getStepValue gets the step of between the lower and upper value. step = (upper-lower)/num.
// Convert byte slice to uint64 first.
func getStepValue(lower, upper []byte, num int) uint64 {
	lowerUint := getUint64FromBytes(lower, 0)
	upperUint := getUint64FromBytes(upper, 0xff)
	return (upperUint - lowerUint) / uint64(num)
}

// getUint64FromBytes gets a uint64 from the `bs` byte slice.
// If len(bs) < 8, then padding with `pad`.
func getUint64FromBytes(bs []byte, pad byte) uint64 {
	buf := bs
	if len(buf) < 8 {
		buf = make([]byte, 0, 8)
		buf = append(buf, bs...)
		for i := len(buf); i < 8; i++ {
			buf = append(buf, pad)
		}
	}
	return binary.BigEndian.Uint64(buf)
}

func datumSliceToString(ds []types.Datum) (string, error) {
	str := "("
	for i, d := range ds {
		s, err := d.ToString()
		if err != nil {
			return str, err
		}
		if i > 0 {
			str += ","
		}
		str += s
	}
	str += ")"
	return str, nil
}

// SplitTableRegionExec represents a split table regions executor.
type SplitTableRegionExec struct {
	baseExecutor

	tableInfo  *model.TableInfo
	lower      types.Datum
	upper      types.Datum
	num        int
	valueLists [][]types.Datum
}

// Next implements the Executor Next interface.
func (e *SplitTableRegionExec) Next(ctx context.Context, _ *chunk.Chunk) error {
	store := e.ctx.GetStore()
	s, ok := store.(kv.SplitableStore)
	if !ok {
		return nil
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, e.ctx.GetSessionVars().GetSplitRegionTimeout())
	defer cancel()

	splitKeys, err := e.getSplitTableKeys()
	if err != nil {
		return err
	}
	regionIDs := make([]uint64, 0, len(splitKeys))
	for _, key := range splitKeys {
		regionID, err := s.SplitRegion(key, true)
		if err != nil {
			logutil.Logger(context.Background()).Warn("split table region failed",
				zap.String("table", e.tableInfo.Name.L),
				zap.Error(err))
			continue
		}
		regionIDs = append(regionIDs, regionID)

		failpoint.Inject("mockSplitRegionTimeout", func(val failpoint.Value) {
			if val.(bool) {
				time.Sleep(time.Second * 1)
			}
		})

		if isCtxDone(ctxWithTimeout) {
			return errors.Errorf("split region timeout(%v)", e.ctx.GetSessionVars().GetSplitRegionTimeout())
		}
	}
	if !e.ctx.GetSessionVars().WaitSplitRegionFinish {
		return nil
	}
	for _, regionID := range regionIDs {
		err := s.WaitScatterRegionFinish(regionID)
		if err != nil {
			logutil.Logger(context.Background()).Warn("wait scatter region failed",
				zap.Uint64("regionID", regionID),
				zap.String("table", e.tableInfo.Name.L),
				zap.Error(err))
		}

		failpoint.Inject("mockScatterRegionTimeout", func(val failpoint.Value) {
			if val.(bool) {
				time.Sleep(time.Second * 1)
			}
		})

		if isCtxDone(ctxWithTimeout) {
			return errors.Errorf("wait split region scatter timeout(%v)", e.ctx.GetSessionVars().GetSplitRegionTimeout())
		}
	}
	return nil
}

func isCtxDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

var minRegionStepValue = uint64(1000)

func (e *SplitTableRegionExec) getSplitTableKeys() ([][]byte, error) {
	var keys [][]byte
	if e.num > 0 {
		keys = make([][]byte, 0, e.num)
	} else {
		keys = make([][]byte, 0, len(e.valueLists))
	}
	recordPrefix := tablecodec.GenTableRecordPrefix(e.tableInfo.ID)
	if len(e.valueLists) > 0 {
		for _, v := range e.valueLists {
			key := tablecodec.EncodeRecordKey(recordPrefix, v[0].GetInt64())
			keys = append(keys, key)
		}
		return keys, nil
	}
	isUnsigned := false
	if e.tableInfo.PKIsHandle {
		if pkCol := e.tableInfo.GetPkColInfo(); pkCol != nil {
			isUnsigned = mysql.HasUnsignedFlag(pkCol.Flag)
		}
	}
	var step uint64
	var lowerValue int64
	if isUnsigned {
		lowerRecordID := e.lower.GetUint64()
		upperRecordID := e.upper.GetUint64()
		if upperRecordID <= lowerRecordID {
			return nil, errors.Errorf("Split table `%s` region lower value %v should less than the upper value %v", e.tableInfo.Name, lowerRecordID, upperRecordID)
		}
		step = (upperRecordID - lowerRecordID) / uint64(e.num)
		lowerValue = int64(lowerRecordID)
	} else {
		lowerRecordID := e.lower.GetInt64()
		upperRecordID := e.upper.GetInt64()
		if upperRecordID <= lowerRecordID {
			return nil, errors.Errorf("Split table `%s` region lower value %v should less than the upper value %v", e.tableInfo.Name, lowerRecordID, upperRecordID)
		}
		step = uint64(upperRecordID-lowerRecordID) / uint64(e.num)
		lowerValue = lowerRecordID
	}
	if step < minRegionStepValue {
		return nil, errors.Errorf("Split table `%s` region step value should more than %v, step %v is invalid", e.tableInfo.Name, minRegionStepValue, step)
	}

	recordID := lowerValue
	for i := 1; i < e.num; i++ {
		recordID += int64(step)
		key := tablecodec.EncodeRecordKey(recordPrefix, recordID)
		keys = append(keys, key)
	}
	return keys, nil
}

// RegionMeta contains a region's peer detail
type regionMeta struct {
	region     *metapb.Region
	leaderID   uint64
	storeID    uint64 // storeID is the store ID of the leader region.
	start      string
	end        string
	scattering bool
}

func getPhysicalTableRegions(physicalTableID int64, tableInfo *model.TableInfo, tikvStore tikv.Storage, s kv.SplitableStore, uniqueRegionMap map[uint64]struct{}) ([]regionMeta, error) {
	if uniqueRegionMap == nil {
		uniqueRegionMap = make(map[uint64]struct{})
	}
	// for record
	startKey, endKey := tablecodec.GetTableHandleKeyRange(physicalTableID)
	regionCache := tikvStore.GetRegionCache()
	recordRegionMetas, err := regionCache.LoadRegionsInKeyRange(tikv.NewBackoffer(context.Background(), 20000), startKey, endKey)
	if err != nil {
		return nil, err
	}
	recordPrefix := tablecodec.GenTableRecordPrefix(physicalTableID)
	tablePrefix := tablecodec.GenTablePrefix(physicalTableID)
	recordRegions, err := getRegionMeta(recordRegionMetas, uniqueRegionMap, tablePrefix, recordPrefix, nil, physicalTableID, 0)
	if err != nil {
		return nil, err
	}

	regions := recordRegions
	// for indices
	for _, index := range tableInfo.Indices {
		if index.State != model.StatePublic {
			continue
		}
		startKey, endKey := tablecodec.GetTableIndexKeyRange(physicalTableID, index.ID)
		regionMetas, err := regionCache.LoadRegionsInKeyRange(tikv.NewBackoffer(context.Background(), 20000), startKey, endKey)
		if err != nil {
			return nil, err
		}
		indexPrefix := tablecodec.EncodeTableIndexPrefix(physicalTableID, index.ID)
		indexRegions, err := getRegionMeta(regionMetas, uniqueRegionMap, tablePrefix, recordPrefix, indexPrefix, physicalTableID, index.ID)
		if err != nil {
			return nil, err
		}
		regions = append(regions, indexRegions...)
	}
	err = checkRegionsStatus(s, regions)
	if err != nil {
		return nil, err
	}
	return regions, nil
}

func getPhysicalIndexRegions(physicalTableID int64, indexInfo *model.IndexInfo, tikvStore tikv.Storage, s kv.SplitableStore, uniqueRegionMap map[uint64]struct{}) ([]regionMeta, error) {
	if uniqueRegionMap == nil {
		uniqueRegionMap = make(map[uint64]struct{})
	}

	startKey, endKey := tablecodec.GetTableIndexKeyRange(physicalTableID, indexInfo.ID)
	regionCache := tikvStore.GetRegionCache()
	regions, err := regionCache.LoadRegionsInKeyRange(tikv.NewBackoffer(context.Background(), 20000), startKey, endKey)
	if err != nil {
		return nil, err
	}
	recordPrefix := tablecodec.GenTableRecordPrefix(physicalTableID)
	tablePrefix := tablecodec.GenTablePrefix(physicalTableID)
	indexPrefix := tablecodec.EncodeTableIndexPrefix(physicalTableID, indexInfo.ID)
	indexRegions, err := getRegionMeta(regions, uniqueRegionMap, tablePrefix, recordPrefix, indexPrefix, physicalTableID, indexInfo.ID)
	if err != nil {
		return nil, err
	}
	err = checkRegionsStatus(s, indexRegions)
	if err != nil {
		return nil, err
	}
	return indexRegions, nil
}

func checkRegionsStatus(store kv.SplitableStore, regions []regionMeta) error {
	for i := range regions {
		scattering, err := store.CheckRegionInScattering(regions[i].region.Id)
		if err != nil {
			return err
		}
		regions[i].scattering = scattering
	}
	return nil
}

func decodeRegionsKey(regions []regionMeta, tablePrefix, recordPrefix, indexPrefix []byte, physicalTableID, indexID int64) {
	d := &regionKeyDecoder{
		physicalTableID: physicalTableID,
		tablePrefix:     tablePrefix,
		recordPrefix:    recordPrefix,
		indexPrefix:     indexPrefix,
		indexID:         indexID,
	}
	for i := range regions {
		regions[i].start = d.decodeRegionKey(regions[i].region.StartKey)
		regions[i].end = d.decodeRegionKey(regions[i].region.EndKey)
	}
}

type regionKeyDecoder struct {
	physicalTableID int64
	tablePrefix     []byte
	recordPrefix    []byte
	indexPrefix     []byte
	indexID         int64
}

func (d *regionKeyDecoder) decodeRegionKey(key []byte) string {
	if len(d.indexPrefix) > 0 && bytes.HasPrefix(key, d.indexPrefix) {
		return fmt.Sprintf("t_%d_i_%d_%x", d.physicalTableID, d.indexID, key[len(d.indexPrefix):])
	} else if len(d.recordPrefix) > 0 && bytes.HasPrefix(key, d.recordPrefix) {
		_, handle, err := codec.DecodeInt(key[len(d.recordPrefix):])
		if err == nil {
			return fmt.Sprintf("t_%d_r_%d", d.physicalTableID, handle)
		}
	}
	if len(d.tablePrefix) > 0 && bytes.HasPrefix(key, d.tablePrefix) {
		key = key[len(d.tablePrefix):]
		// Has index prefix.
		if !bytes.HasPrefix(key, []byte("_i")) {
			return fmt.Sprintf("t_%d_%x", d.physicalTableID, key)
		}
		key = key[2:]
		// try to decode index ID.
		if _, indexID, err := codec.DecodeInt(key); err == nil {
			return fmt.Sprintf("t_%d_i_%d_%x", d.physicalTableID, indexID, key[8:])
		}
		return fmt.Sprintf("t_%d_i__%x", d.physicalTableID, key)
	}
	// Has table prefix.
	if bytes.HasPrefix(key, []byte("t")) {
		key = key[1:]
		// try to decode table ID.
		if _, tableID, err := codec.DecodeInt(key); err == nil {
			return fmt.Sprintf("t_%d_%x", tableID, key[8:])
		}
		return fmt.Sprintf("t_%x", key)
	}
	return fmt.Sprintf("%x", key)
}

func getRegionMeta(regionMetas []*tikv.Region, uniqueRegionMap map[uint64]struct{}, tablePrefix, recordPrefix, indexPrefix []byte, physicalTableID, indexID int64) ([]regionMeta, error) {
	regions := make([]regionMeta, 0, len(regionMetas))
	for _, r := range regionMetas {
		if _, ok := uniqueRegionMap[r.GetID()]; ok {
			continue
		}
		uniqueRegionMap[r.GetID()] = struct{}{}
		regions = append(regions, regionMeta{
			region:   r.GetMeta(),
			leaderID: r.GetLeaderID(),
			storeID:  r.GetLeaderStoreID(),
		})
	}
	decodeRegionsKey(regions, tablePrefix, recordPrefix, indexPrefix, physicalTableID, indexID)
	return regions, nil
}
