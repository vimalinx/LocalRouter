package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
)

const maxProtocolWASMBytes = 16 << 20

func validateProtocolWASMAdapter(definition *protocolDefinition, adapter *protocolAdapterConfig) error {
	if adapter.ModuleFile == "" || filepath.IsAbs(adapter.ModuleFile) || filepath.Clean(adapter.ModuleFile) != adapter.ModuleFile || strings.Contains(adapter.ModuleFile, "..") {
		return errors.New("module_file must be a clean relative path")
	}
	expectedPrefix := filepath.Join(definition.ID, "modules") + string(filepath.Separator)
	if !strings.HasPrefix(adapter.ModuleFile, expectedPrefix) || filepath.Ext(adapter.ModuleFile) != ".wasm" {
		return fmt.Errorf("module_file must be under %s/modules and end in .wasm", definition.ID)
	}
	modulePath := filepath.Join(filepath.Dir(definition.SourceFile), adapter.ModuleFile)
	info, err := os.Lstat(modulePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 8 || info.Size() > maxProtocolWASMBytes {
		return errors.New("module_file must be a regular WebAssembly file of at most 16 MiB")
	}
	header := make([]byte, 8)
	file, err := os.Open(modulePath)
	if err != nil {
		return err
	}
	_, readErr := file.Read(header)
	_ = file.Close()
	if readErr != nil || binary.LittleEndian.Uint32(header[:4]) != 0x6d736100 || binary.LittleEndian.Uint32(header[4:]) != 1 {
		return errors.New("module_file does not have a valid WebAssembly v1 header")
	}
	if adapter.Function == "" {
		adapter.Function = "transform"
	}
	if adapter.AllocFunction == "" {
		adapter.AllocFunction = "alloc"
	}
	if adapter.FreeFunction == "" {
		adapter.FreeFunction = "dealloc"
	}
	for name, value := range map[string]string{"function": adapter.Function, "alloc_function": adapter.AllocFunction, "free_function": adapter.FreeFunction} {
		if !protocolOperationIDPattern.MatchString(value) {
			return fmt.Errorf("%s is unsafe", name)
		}
	}
	if adapter.TimeoutMS == 0 {
		adapter.TimeoutMS = 1000
	}
	if adapter.TimeoutMS < 10 || adapter.TimeoutMS > 30000 {
		return errors.New("timeout_ms must be between 10 and 30000")
	}
	if adapter.MaxMemoryPages == 0 {
		adapter.MaxMemoryPages = 256
	}
	if adapter.MaxMemoryPages < 2 || adapter.MaxMemoryPages > 1024 {
		return errors.New("max_memory_pages must be between 2 and 1024")
	}
	return nil
}

// invokeProtocolWASMAdapter executes a capability-free adapter ABI. No WASI or
// host modules are instantiated, so the guest has no filesystem, network,
// clock, environment, or process access. The ABI is:
//
//	alloc(input_len) -> input_ptr
//	transform(input_ptr, input_len) -> (output_ptr << 32 | output_len)
//	dealloc(ptr, len)
func invokeProtocolWASMAdapter(parent context.Context, definition protocolDefinition, adapter protocolAdapterConfig, input []byte) ([]byte, error) {
	modulePath := filepath.Join(filepath.Dir(definition.SourceFile), adapter.ModuleFile)
	wasm, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(adapter.TimeoutMS)*time.Millisecond)
	defer cancel()
	runtimeConfig := wazero.NewRuntimeConfig().WithMemoryLimitPages(adapter.MaxMemoryPages).WithCloseOnContextDone(true)
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		return nil, fmt.Errorf("compile module: %w", err)
	}
	module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("instantiate module: %w", err)
	}
	defer module.Close(ctx)
	alloc := module.ExportedFunction(adapter.AllocFunction)
	transform := module.ExportedFunction(adapter.Function)
	free := module.ExportedFunction(adapter.FreeFunction)
	memory := module.Memory()
	if alloc == nil || transform == nil || free == nil || memory == nil {
		return nil, errors.New("module must export memory, alloc, transform, and dealloc")
	}
	allocated, err := alloc.Call(ctx, uint64(len(input)))
	if err != nil || len(allocated) != 1 {
		return nil, errors.New("adapter allocation failed")
	}
	inputPtr := uint32(allocated[0])
	if !memory.Write(inputPtr, input) {
		return nil, errors.New("adapter input exceeds guest memory")
	}
	results, err := transform.Call(ctx, uint64(inputPtr), uint64(len(input)))
	if err != nil || len(results) != 1 {
		return nil, errors.New("adapter transform failed")
	}
	outputPtr := uint32(results[0] >> 32)
	outputLen := uint32(results[0])
	if outputLen == 0 || outputLen > maxProtocolBodyBytes {
		return nil, errors.New("adapter output size is invalid")
	}
	output, ok := memory.Read(outputPtr, outputLen)
	if !ok {
		return nil, errors.New("adapter output exceeds guest memory")
	}
	result := append([]byte(nil), output...)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, _ = free.Call(cleanupCtx, uint64(outputPtr), uint64(outputLen))
	_, _ = free.Call(cleanupCtx, uint64(inputPtr), uint64(len(input)))
	cleanupCancel()
	return result, nil
}
