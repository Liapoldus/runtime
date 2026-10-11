// Package wasm executes isolated bounded Runtime modules.
package wasm

// rejectModuleImports permits only the single versioned Runtime host function.
func rejectModuleImports(module []byte) error {
	if len(module) < 8 {
		return ErrInvalidABI
	}
	offset := 8 // WASM magic and version
	foundImportSection := false
	for offset < len(module) {
		sectionID := module[offset]
		offset++
		sectionSize, err := readWasmUint32(module, &offset, len(module))
		if err != nil || int64(sectionSize) > int64(len(module)-offset) {
			return ErrInvalidABI
		}
		sectionEnd := offset + int(sectionSize)
		if sectionID == 2 {
			if foundImportSection {
				return ErrInvalidABI
			}
			foundImportSection = true
			importCount, err := readWasmUint32(module, &offset, sectionEnd)
			if err != nil || importCount > 1 {
				return ErrInvalidABI
			}
			if importCount == 1 {
				moduleName, err := readWasmName(module, &offset, sectionEnd)
				if err != nil {
					return ErrInvalidABI
				}
				fieldName, err := readWasmName(module, &offset, sectionEnd)
				if err != nil || offset >= sectionEnd || moduleName != "liapoldus.plugin.v2" || fieldName != "call" || module[offset] != 0 {
					return ErrInvalidABI
				}
				offset++
				if _, err := readWasmUint32(module, &offset, sectionEnd); err != nil {
					return ErrInvalidABI
				}
			}
			if offset != sectionEnd {
				return ErrInvalidABI
			}
		}
		offset = sectionEnd
	}
	return nil
}

func readWasmName(module []byte, offset *int, limit int) (string, error) {
	length, err := readWasmUint32(module, offset, limit)
	if err != nil || int64(length) > int64(limit-*offset) {
		return "", ErrInvalidABI
	}
	end := *offset + int(length)
	name := string(module[*offset:end])
	*offset = end
	return name, nil
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
