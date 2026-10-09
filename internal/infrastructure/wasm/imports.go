// Package wasm executes isolated bounded Runtime modules.
package wasm

// rejectModuleImports enforces the current ABI's empty hostModules allow-list.
// CompiledModule exposes imported functions and memories, but not imported
// tables or globals, so the import vector must also be inspected before the
// module is admitted or instantiated.
func rejectModuleImports(module []byte) error {
	hasImports, err := moduleHasImports(module)
	if err != nil || hasImports {
		return ErrInvalidABI
	}
	return nil
}

func moduleHasImports(module []byte) (bool, error) {
	if len(module) < 8 {
		return false, ErrInvalidABI
	}
	offset := 8 // WASM magic and version
	for offset < len(module) {
		sectionID := module[offset]
		offset++
		sectionSize, err := readWasmUint32(module, &offset, len(module))
		if err != nil || int64(sectionSize) > int64(len(module)-offset) {
			return false, ErrInvalidABI
		}
		sectionEnd := offset + int(sectionSize)
		if sectionID == 2 {
			importCount, err := readWasmUint32(module, &offset, sectionEnd)
			if err != nil || offset > sectionEnd {
				return false, ErrInvalidABI
			}
			return importCount != 0, nil
		}
		offset = sectionEnd
	}
	return false, nil
}

func readWasmUint32(module []byte, offset *int, limit int) (uint32, error) {
	var value uint32
	for shift := uint(0); shift < 35; shift += 7 {
		if *offset >= limit {
			return 0, ErrInvalidABI
		}
		current := module[*offset]
		*offset = *offset + 1
		if shift == 28 && current&0xf0 != 0 {
			return 0, ErrInvalidABI
		}
		value |= uint32(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, nil
		}
	}
	return 0, ErrInvalidABI
}
