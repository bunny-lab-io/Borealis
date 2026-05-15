//go:build windows

package processmanagement

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	afInet                  = 2
	tcpTableOwnerPIDAll     = 5
	tcpStateEstablished     = 5
	tcpConnectionEstatsData = 1
)

var (
	iphlpapi                     = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable      = iphlpapi.NewProc("GetExtendedTcpTable")
	procSetPerTCPConnectionStats = iphlpapi.NewProc("SetPerTcpConnectionEStats")
	procGetPerTCPConnectionStats = iphlpapi.NewProc("GetPerTcpConnectionEStats")
)

type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

type mibTCPRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

type tcpEstatsDataRW struct {
	EnableCollection uint8
}

type tcpEstatsDataROD struct {
	DataBytesOut      uint64
	DataSegsOut       uint64
	DataBytesIn       uint64
	DataSegsIn        uint64
	SegsOut           uint64
	SegsIn            uint64
	SoftErrors        uint32
	SoftErrorReason   uint32
	SndUna            uint32
	SndNxt            uint32
	SndMax            uint32
	ThruBytesAcked    uint64
	RcvNxt            uint32
	ThruBytesReceived uint64
}

func collectPlatformNetworkRates(_ context.Context, previous map[string]rateCounter) (map[int]float64, map[string]rateCounter) {
	rows, err := windowsTCPRows()
	if err != nil {
		return map[int]float64{}, map[string]rateCounter{}
	}
	now := time.Now()
	next := map[string]rateCounter{}
	rates := map[int]float64{}
	for _, ownerRow := range rows {
		if ownerRow.State != tcpStateEstablished || ownerRow.OwningPID == 0 {
			continue
		}
		total, ok := windowsTCPByteTotal(ownerRow)
		if !ok {
			continue
		}
		pid := int(ownerRow.OwningPID)
		key := fmt.Sprintf("tcp4:%d:%d:%d:%d:%d:%d", ownerRow.LocalAddr, ownerRow.LocalPort, ownerRow.RemoteAddr, ownerRow.RemotePort, ownerRow.State, pid)
		current := rateCounter{At: now, Total: int64(total)}
		next[key] = current
		if prev, found := previous[key]; found {
			rates[pid] += bytesPerSecond(prev, current)
		}
	}
	return rates, next
}

func windowsTCPRows() ([]mibTCPRowOwnerPID, error) {
	var size uint32
	r1, _, _ := procGetExtendedTCPTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(afInet),
		uintptr(tcpTableOwnerPIDAll),
		0,
	)
	if r1 != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && size == 0 {
		return nil, windows.Errno(r1)
	}
	buffer := make([]byte, size)
	r1, _, _ = procGetExtendedTCPTable.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(afInet),
		uintptr(tcpTableOwnerPIDAll),
		0,
	)
	if r1 != 0 {
		return nil, windows.Errno(r1)
	}
	count := *(*uint32)(unsafe.Pointer(&buffer[0]))
	rows := make([]mibTCPRowOwnerPID, 0, count)
	offset := uintptr(unsafe.Sizeof(count))
	rowSize := unsafe.Sizeof(mibTCPRowOwnerPID{})
	for index := uint32(0); index < count; index++ {
		row := *(*mibTCPRowOwnerPID)(unsafe.Pointer(&buffer[offset+uintptr(index)*rowSize]))
		rows = append(rows, row)
	}
	return rows, nil
}

func windowsTCPByteTotal(ownerRow mibTCPRowOwnerPID) (uint64, bool) {
	row := mibTCPRow{
		State:      ownerRow.State,
		LocalAddr:  ownerRow.LocalAddr,
		LocalPort:  ownerRow.LocalPort,
		RemoteAddr: ownerRow.RemoteAddr,
		RemotePort: ownerRow.RemotePort,
	}
	rw := tcpEstatsDataRW{EnableCollection: 1}
	_, _, _ = procSetPerTCPConnectionStats.Call(
		uintptr(unsafe.Pointer(&row)),
		uintptr(tcpConnectionEstatsData),
		uintptr(unsafe.Pointer(&rw)),
		0,
		unsafe.Sizeof(rw),
		0,
	)
	var rwProbe tcpEstatsDataRW
	var rod tcpEstatsDataROD
	r1, _, _ := procGetPerTCPConnectionStats.Call(
		uintptr(unsafe.Pointer(&row)),
		uintptr(tcpConnectionEstatsData),
		uintptr(unsafe.Pointer(&rwProbe)),
		0,
		unsafe.Sizeof(rwProbe),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&rod)),
		0,
		unsafe.Sizeof(rod),
	)
	if r1 != 0 || rwProbe.EnableCollection == 0 {
		return 0, false
	}
	return rod.DataBytesIn + rod.DataBytesOut, true
}
