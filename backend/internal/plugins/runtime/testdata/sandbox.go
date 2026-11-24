package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/extism/go-pdk"
)

//go:wasmimport wasi_snapshot_preview1 sock_accept
func sockAccept(fd uint32, flags uint32, accepted *uint32) uint32

//go:wasmexport describe
func describe() uint32 {
	pdk.OutputString(`{"capabilities":["lyrics"]}`)
	return 0
}

//go:wasmexport check_wasi
func checkWASI() uint32 {
	// 路径和变量名由测试提供，只报告合成文件与变量是否可见。
	path, key, ok := strings.Cut(pdk.InputString(), "\n")
	if !ok {
		return 3
	}
	f, err := os.Open(path)
	if err == nil {
		f.Close()
	}
	_, present := os.LookupEnv(key)
	var accepted uint32
	pdk.OutputString(`{"env_visible":` + strconv.FormatBool(present) +
		`,"file_visible":` + strconv.FormatBool(err == nil) +
		`,"socket_visible":` + strconv.FormatBool(sockAccept(0, 0, &accepted) == 0) + "}")
	return 0
}

//go:wasmexport output_boundary
func outputBoundary() uint32 {
	n, err := strconv.Atoi(pdk.InputString())
	if err != nil || n < 0 || n > 256<<10 {
		return 3
	}
	pdk.OutputString("\"" + strings.Repeat("a", n) + "\"")
	return 0
}

//go:wasmexport memory_boundary
func memoryBoundary() uint32 {
	mib, err := strconv.Atoi(pdk.InputString())
	if err != nil || mib < 1 || mib > 33 {
		return 3
	}
	data := make([]byte, mib<<20)
	data[len(data)-1] = 1
	runtime.KeepAlive(data)
	pdk.OutputString(`{}`)
	return 0
}

func main() {}

//go:wasmexport spin
func spin() uint32 {
	for {
	}
}
