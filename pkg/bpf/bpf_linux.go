// Copyright 2016-2019 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build linux

package bpf

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/spanstat"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// CreateMap creates a Map of type mapType, with key size keySize, a value size of
// valueSize and the maximum amount of entries of maxEntries.
// mapType should be one of the bpf_map_type in "uapi/linux/bpf.h"
// When mapType is the type HASH_OF_MAPS an innerID is required to point at a
// map fd which has the same type/keySize/valueSize/maxEntries as expected map
// entries. For all other mapTypes innerID is ignored and should be zeroed.
func CreateMap(mapType int, keySize, valueSize, maxEntries, flags, innerID uint32, path string) (int, error) {
	// This struct must be in sync with union bpf_attr's anonymous struct
	// used by the BPF_MAP_CREATE command
	uba := struct {
		mapType    uint32
		keySize    uint32
		valueSize  uint32
		maxEntries uint32
		mapFlags   uint32
		innerID    uint32
	}{
		uint32(mapType),
		keySize,
		valueSize,
		maxEntries,
		flags,
		innerID,
	}

	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_CREATE,
		uintptr(unsafe.Pointer(&uba)),
		unsafe.Sizeof(uba),
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpCreate, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if err != 0 {
		return 0, &os.PathError{
			Op:   "Unable to create map",
			Path: path,
			Err:  err,
		}
	}

	return int(ret), nil
}

// This struct must be in sync with union bpf_attr's anonymous struct used by
// BPF_MAP_*_ELEM commands
type bpfAttrMapOpElem struct {
	mapFd uint32
	pad0  [4]byte
	key   uint64
	value uint64 // union: value or next_key
	flags uint64
}

// UpdateElementFromPointers updates the map in fd with the given value in the given key.
// The flags can have the following values:
// bpf.BPF_ANY to create new element or update existing;
// bpf.BPF_NOEXIST to create new element if it didn't exist;
// bpf.BPF_EXIST to update existing element.
func UpdateElementFromPointers(fd int, structPtr, sizeOfStruct uintptr) error {
	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_UPDATE_ELEM,
		structPtr,
		sizeOfStruct,
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpUpdate, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if ret != 0 || err != 0 {
		return fmt.Errorf("Unable to update element for map with file descriptor %d: %s", fd, err)
	}

	return nil
}

// UpdateElement updates the map in fd with the given value in the given key.
// The flags can have the following values:
// bpf.BPF_ANY to create new element or update existing;
// bpf.BPF_NOEXIST to create new element if it didn't exist;
// bpf.BPF_EXIST to update existing element.
// Deprecated, use UpdateElementFromPointers
func UpdateElement(fd int, key, value unsafe.Pointer, flags uint64) error {
	uba := bpfAttrMapOpElem{
		mapFd: uint32(fd),
		key:   uint64(uintptr(key)),
		value: uint64(uintptr(value)),
		flags: uint64(flags),
	}

	return UpdateElementFromPointers(fd, uintptr(unsafe.Pointer(&uba)), unsafe.Sizeof(uba))
}

// LookupElement looks up for the map value stored in fd with the given key. The value
// is stored in the value unsafe.Pointer.
func LookupElementFromPointers(fd int, structPtr, sizeOfStruct uintptr) error {
	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_LOOKUP_ELEM,
		structPtr,
		sizeOfStruct,
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpLookup, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if ret != 0 || err != 0 {
		return fmt.Errorf("Unable to lookup element in map with file descriptor %d: %s", fd, err)
	}

	return nil
}

// LookupElement looks up for the map value stored in fd with the given key. The value
// is stored in the value unsafe.Pointer.
// Deprecated, use LookupElementFromPointers
func LookupElement(fd int, key, value unsafe.Pointer) error {
	uba := bpfAttrMapOpElem{
		mapFd: uint32(fd),
		key:   uint64(uintptr(key)),
		value: uint64(uintptr(value)),
	}

	return LookupElementFromPointers(fd, uintptr(unsafe.Pointer(&uba)), unsafe.Sizeof(uba))
}

func deleteElement(fd int, key unsafe.Pointer) (uintptr, syscall.Errno) {
	uba := bpfAttrMapOpElem{
		mapFd: uint32(fd),
		key:   uint64(uintptr(key)),
	}
	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_DELETE_ELEM,
		uintptr(unsafe.Pointer(&uba)),
		unsafe.Sizeof(uba),
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpDelete, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	return ret, err
}

// DeleteElement deletes the map element with the given key.
func DeleteElement(fd int, key unsafe.Pointer) error {
	ret, err := deleteElement(fd, key)

	if ret != 0 || err != 0 {
		return fmt.Errorf("Unable to delete element from map with file descriptor %d: %s", fd, err)
	}

	return nil
}

// GetNextKeyFromPointers stores, in nextKey, the next key after the key of the map in fd.
func GetNextKeyFromPointers(fd int, structPtr, sizeOfStruct uintptr) error {

	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_GET_NEXT_KEY,
		structPtr,
		sizeOfStruct,
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpGetNextKey, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if ret != 0 || err != 0 {
		return fmt.Errorf("Unable to get next key from map with file descriptor %d: %s", fd, err)
	}

	return nil
}

// GetNextKey stores, in nextKey, the next key after the key of the map in fd.
// Deprecated, use GetNextKeyFromPointers
func GetNextKey(fd int, key, nextKey unsafe.Pointer) error {
	uba := bpfAttrMapOpElem{
		mapFd: uint32(fd),
		key:   uint64(uintptr(key)),
		value: uint64(uintptr(nextKey)),
	}

	return GetNextKeyFromPointers(fd, uintptr(unsafe.Pointer(&uba)), unsafe.Sizeof(uba))
}

// This struct must be in sync with union bpf_attr's anonymous struct used by
// BPF_OBJ_*_ commands
type bpfAttrObjOp struct {
	pathname uint64
	fd       uint32
	pad0     [4]byte
}

// ObjPin stores the map's fd in pathname.
func ObjPin(fd int, pathname string) error {
	pathStr := syscall.StringBytePtr(pathname)
	uba := bpfAttrObjOp{
		pathname: uint64(uintptr(unsafe.Pointer(pathStr))),
		fd:       uint32(fd),
	}

	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	ret, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_OBJ_PIN,
		uintptr(unsafe.Pointer(&uba)),
		unsafe.Sizeof(uba),
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpObjPin, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if ret != 0 || err != 0 {
		return fmt.Errorf("Unable to pin object with file descriptor %d to %s: %s", fd, pathname, err)
	}

	return nil
}

// ObjGet reads the pathname and returns the map's fd read.
func ObjGet(pathname string) (int, error) {
	pathStr := syscall.StringBytePtr(pathname)
	uba := bpfAttrObjOp{
		pathname: uint64(uintptr(unsafe.Pointer(pathStr))),
	}

	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	fd, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_OBJ_GET,
		uintptr(unsafe.Pointer(&uba)),
		unsafe.Sizeof(uba),
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpObjGet, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if fd == 0 || err != 0 {
		return 0, &os.PathError{
			Op:   "Unable to get object",
			Err:  err,
			Path: pathname,
		}
	}

	return int(fd), nil
}

type bpfAttrFdFromId struct {
	ID     uint32
	NextID uint32
	Flags  uint32
}

// MapFdFromID retrieves a file descriptor based on a map ID.
func MapFdFromID(id int) (int, error) {
	uba := bpfAttrFdFromId{
		ID: uint32(id),
	}

	var duration *spanstat.SpanStat
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		duration = spanstat.Start()
	}
	fd, _, err := unix.Syscall(
		unix.SYS_BPF,
		BPF_MAP_GET_FD_BY_ID,
		uintptr(unsafe.Pointer(&uba)),
		unsafe.Sizeof(uba),
	)
	if option.Config.MetricsConfig.BPFSyscallDurationEnabled {
		metrics.BPFSyscallDuration.WithLabelValues(metricOpGetFDByID, metrics.Errno2Outcome(err)).Observe(duration.End(err == 0).Total().Seconds())
	}

	if fd == 0 || err != 0 {
		return 0, fmt.Errorf("Unable to get object fd from id %d: %s", id, err)
	}

	return int(fd), nil
}

// ObjClose closes the map's fd.
func ObjClose(fd int) error {
	if fd > 0 {
		return unix.Close(fd)
	}
	return nil
}

func objCheck(fd int, path string, mapType int, keySize, valueSize, maxEntries, flags uint32) bool {
	info, err := GetMapInfo(os.Getpid(), fd)
	if err != nil {
		return false
	}

	scopedLog := log.WithField(logfields.Path, path)
	mismatch := false

	if int(info.MapType) != mapType {
		scopedLog.WithFields(logrus.Fields{
			"old": info.MapType,
			"new": MapType(mapType),
		}).Warning("Map type mismatch for BPF map")
		mismatch = true
	}

	if info.KeySize != keySize {
		scopedLog.WithFields(logrus.Fields{
			"old": info.KeySize,
			"new": keySize,
		}).Warning("Key-size mismatch for BPF map")
		mismatch = true
	}

	if info.ValueSize != valueSize {
		scopedLog.WithFields(logrus.Fields{
			"old": info.ValueSize,
			"new": valueSize,
		}).Warning("Value-size mismatch for BPF map")
		mismatch = true
	}

	if info.MaxEntries != maxEntries {
		scopedLog.WithFields(logrus.Fields{
			"old": info.MaxEntries,
			"new": maxEntries,
		}).Warning("Max entries mismatch for BPF map")
		mismatch = true
	}
	if info.Flags != flags {
		scopedLog.WithFields(logrus.Fields{
			"old": info.Flags,
			"new": flags,
		}).Warning("Flags mismatch for BPF map")
		mismatch = true
	}

	if mismatch {
		if info.MapType == MapTypeProgArray {
			return false
		}

		scopedLog.Warning("Removing map to allow for property upgrade (expect map data loss)")

		// Kernel still holds map reference count via attached prog.
		// Only exception is prog array, but that is already resolved
		// differently.
		os.Remove(path)
		return true
	}

	return false
}

func OpenOrCreateMap(path string, mapType int, keySize, valueSize, maxEntries, flags uint32, innerID uint32) (int, bool, error) {
	var fd int

	redo := false
	isNewMap := false

recreate:
	if _, err := os.Stat(path); os.IsNotExist(err) || redo {
		mapDir := filepath.Dir(path)
		if _, err = os.Stat(mapDir); os.IsNotExist(err) {
			if err = os.MkdirAll(mapDir, 0755); err != nil {
				return 0, isNewMap, &os.PathError{
					Op:   "Unable create map base directory",
					Path: path,
					Err:  err,
				}
			}
		}

		fd, err = CreateMap(
			mapType,
			keySize,
			valueSize,
			maxEntries,
			flags,
			innerID,
			path,
		)

		defer func() {
			if err != nil {
				// In case of error, we need to close
				// this fd since it was open by CreateMap
				ObjClose(fd)
			}
		}()

		isNewMap = true

		if err != nil {
			return 0, isNewMap, err
		}

		err = ObjPin(fd, path)
		if err != nil {
			return 0, isNewMap, err
		}

		return fd, isNewMap, nil
	}

	fd, err := ObjGet(path)
	if err == nil {
		redo = objCheck(
			fd,
			path,
			mapType,
			keySize,
			valueSize,
			maxEntries,
			flags,
		)
		if redo == true {
			ObjClose(fd)
			goto recreate
		}
	}

	return fd, isNewMap, err
}

// GetMtime returns monotonic time that can be used to compare
// values with ktime_get_ns() BPF helper, e.g. needed to check
// the timeout in sec for BPF entries. We return the raw nsec,
// although that is not quite usable for comparison. Go has
// runtime.nanotime() but doesn't expose it as API.
func GetMtime() (uint64, error) {
	var ts unix.Timespec

	err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	if err != nil {
		return 0, fmt.Errorf("Unable get time: %s", err)
	}

	return uint64(unix.TimespecToNsec(ts)), nil
}

type bpfAttrProg struct {
	ProgType    uint32
	InsnCnt     uint32
	Insns       uintptr
	License     uintptr
	LogLevel    uint32
	LogSize     uint32
	LogBuf      uintptr
	KernVersion uint32
	Flags       uint32
	Name        [16]byte
	Ifindex     uint32
	AttachType  uint32
}

// TestDummyProg loads a minimal BPF program into the kernel and probes
// whether it succeeds in doing so. This can be used to bail out early
// in the daemon when a given type is not supported.
func TestDummyProg(progType ProgType, attachType uint32) error {
	var oldLim unix.Rlimit
	insns := []byte{
		// R0 = 1; EXIT
		0xb7, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
		0x95, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	license := []byte{'A', 'S', 'L', '2', '\x00'}
	bpfAttr := bpfAttrProg{
		ProgType:   uint32(progType),
		AttachType: uint32(attachType),
		InsnCnt:    uint32(len(insns) / 8),
		Insns:      uintptr(unsafe.Pointer(&insns[0])),
		License:    uintptr(unsafe.Pointer(&license[0])),
	}
	tmpLim := unix.Rlimit{
		Cur: math.MaxUint64,
		Max: math.MaxUint64,
	}
	err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &oldLim)
	if err != nil {
		return err
	}
	err = unix.Setrlimit(unix.RLIMIT_MEMLOCK, &tmpLim)
	if err != nil {
		return err
	}
	fd, _, errno := unix.Syscall(unix.SYS_BPF, BPF_PROG_LOAD,
		uintptr(unsafe.Pointer(&bpfAttr)),
		unsafe.Sizeof(bpfAttr))
	err = unix.Setrlimit(unix.RLIMIT_MEMLOCK, &oldLim)
	if errno == 0 {
		unix.Close(int(fd))
		if err != nil {
			return err
		}
		return nil
	}
	return errno
}
