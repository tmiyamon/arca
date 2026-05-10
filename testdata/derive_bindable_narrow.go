//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"errors"
	"fmt"
	"github.com/tmiyamon/arca/stdlib"
	"math"
	"os"
)

type Sample struct {
	Id    int32
	Count uint8
	Ratio float32
}

type SampleDraft struct {
	Id    stdlib.BindableSlot[int64]
	Count stdlib.BindableSlot[uint64]
	Ratio stdlib.BindableSlot[float64]
}

func main() {
	if err := func() error {
		d := sampleDraft()
		_, __err1 := d.Freeze()
		if __err1 != nil {
			return __err1
		}
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (d SampleDraft) Freeze() (Sample, error) {
	if d.Id.Set == false {
		return Sample{}, errors.New("Sample.id is unset")
	}
	if d.Count.Set == false {
		return Sample{}, errors.New("Sample.count is unset")
	}
	if d.Ratio.Set == false {
		return Sample{}, errors.New("Sample.ratio is unset")
	}
	__narrow0, __narrowErr0 := NewInt32(d.Id.Value)
	if __narrowErr0 != nil {
		return Sample{}, __narrowErr0
	}
	__narrow1, __narrowErr1 := NewUInt8(d.Count.Value)
	if __narrowErr1 != nil {
		return Sample{}, __narrowErr1
	}
	__narrow2, __narrowErr2 := NewFloat32(d.Ratio.Value)
	if __narrowErr2 != nil {
		return Sample{}, __narrowErr2
	}
	return Sample{Id: __narrow0, Count: __narrow1, Ratio: __narrow2}, nil
}

func sampleDraft() SampleDraft {
	return SampleDraft{}
}

var __SampleBindable = stdlib.BindableDict[Sample, SampleDraft]{Draft: sampleDraft, Freeze: SampleDraft.Freeze}

func NewInt32(v int64) (int32, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fmt.Errorf("Int32: value %d out of range [%d, %d]", v, int64(math.MinInt32), int64(math.MaxInt32))
	}
	return int32(v), nil
}

func NewUInt8(v uint64) (uint8, error) {
	if v > math.MaxUint8 {
		return 0, fmt.Errorf("UInt8: value %d out of range [0, %d]", v, uint64(math.MaxUint8))
	}
	return uint8(v), nil
}

func NewFloat32(v float64) (float32, error) {
	f := float32(v)
	if math.IsInf(float64(f), 0) && !math.IsInf(v, 0) {
		return 0, fmt.Errorf("Float32: value %g out of range", v)
	}
	return f, nil
}
