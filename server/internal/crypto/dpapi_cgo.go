//go:build windows && cgo

package crypto

/*
#cgo LDFLAGS: -lCrypt32 -lAdvapi32
#include <windows.h>
#include <dpapi.h>

static BOOL DPAPIProtect(
    const BYTE *pbData,
    DWORD cbData,
    BYTE **ppbOut,
    DWORD *pcbOut
) {
    DATA_BLOB dataIn;
    DATA_BLOB dataOut;

    dataIn.pbData = pbData;
    dataIn.cbData = cbData;

    if (!CryptProtectData(&dataIn, NULL, NULL, NULL, NULL, 0, &dataOut)) {
        return FALSE;
    }

    *ppbOut = dataOut.pbData;
    *pcbOut = dataOut.cbData;
    return TRUE;
}

static BOOL DPAPIUnprotect(
    const BYTE *pbData,
    DWORD cbData,
    BYTE **ppbOut,
    DWORD *pcbOut
) {
    DATA_BLOB dataIn;
    DATA_BLOB dataOut;

    dataIn.pbData = pbData;
    dataIn.cbData = cbData;

    if (!CryptUnprotectData(&dataIn, NULL, NULL, NULL, NULL, 0, &dataOut)) {
        return FALSE;
    }

    *ppbOut = dataOut.pbData;
    *pcbOut = dataOut.cbData;
    return TRUE;
}

static void DPAPIFree(void *ptr) {
    LocalFree(ptr);
}

static DWORD DPAPIGetLastError() {
    return GetLastError();
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

// DPAPISource uses Windows Data Protection API for key encryption.
type DPAPISource struct {
	blobPath string
}

func NewDPAPISource(blobPath string) *DPAPISource {
	return &DPAPISource{blobPath: blobPath}
}

func (s *DPAPISource) Name() string {
	return "dpapi"
}

func (s *DPAPISource) GetKEK(_ context.Context) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI not yet fully implemented on Windows")
}

func protect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("dpapi: cannot protect empty data")
	}

	var ppbOut *C.BYTE
	var pcbOut C.DWORD

	result := C.DPAPIProtect(
		(*C.BYTE)(unsafe.Pointer(&data[0])),
		C.DWORD(len(data)),
		&ppbOut,
		&pcbOut,
	)
	if result == 0 {
		return nil, fmt.Errorf("dpapi: protect failed: %w", getLastError())
	}

	defer C.DPAPIFree(unsafe.Pointer(ppbOut))

	out := make([]byte, int(pcbOut))
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(ppbOut)), int(pcbOut)))

	return out, nil
}

func unprotect(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("dpapi: cannot unprotect empty data")
	}

	var ppbOut *C.BYTE
	var pcbOut C.DWORD

	result := C.DPAPIUnprotect(
		(*C.BYTE)(unsafe.Pointer(&blob[0])),
		C.DWORD(len(blob)),
		&ppbOut,
		&pcbOut,
	)
	if result == 0 {
		return nil, fmt.Errorf("dpapi: unprotect failed: %w", getLastError())
	}

	defer C.DPAPIFree(unsafe.Pointer(ppbOut))

	out := make([]byte, int(pcbOut))
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(ppbOut)), int(pcbOut)))

	return out, nil
}

func getLastError() error {
	code := C.DPAPIGetLastError()
	return fmt.Errorf("Windows error code %d", uint32(code))
}
