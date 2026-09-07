package config

import (
	"encoding/binary"
	"testing"
)

func darwinACLFixture(t *testing.T, entries ...[2]uint32) []byte {
	t.Helper()
	length := fileSecurityHeaderSize + len(entries)*aceSize
	buffer := make([]byte, attributeHeaderSize+length)
	binary.LittleEndian.PutUint32(buffer[attributeLengthOffset:aclReferenceOffset], uint32(len(buffer)))
	binary.LittleEndian.PutUint32(buffer[aclReferenceOffset:aclReferenceLengthOffset], attributeHeaderSize-aclReferenceOffset)
	binary.LittleEndian.PutUint32(buffer[aclReferenceLengthOffset:attributeHeaderSize], uint32(length))
	security := buffer[attributeHeaderSize:]
	binary.LittleEndian.PutUint32(security[:4], fileSecurityMagic)
	binary.LittleEndian.PutUint32(security[aclCountOffset:aclFlagsOffset], uint32(len(entries)))
	for index, entry := range entries {
		start := fileSecurityHeaderSize + index*aceSize
		binary.LittleEndian.PutUint32(security[start+aceFlagsOffset:start+aceRightsOffset], entry[0])
		binary.LittleEndian.PutUint32(security[start+aceRightsOffset:start+aceSize], entry[1])
	}
	return buffer
}

func TestValidateDarwinACLBuffer(t *testing.T) {
	empty := make([]byte, attributeHeaderSize)
	binary.LittleEndian.PutUint32(empty[:4], attributeHeaderSize)
	binary.LittleEndian.PutUint32(empty[4:8], attributeHeaderSize-aclReferenceOffset)

	tests := []struct {
		name        string
		buffer      []byte
		privateFile bool
		wantErr     bool
	}{
		{"no ACL", empty, true, false},
		{"deny only", darwinACLFixture(t, [2]uint32{aceDeny, rightDelete}), true, false},
		{"directory read allowed", darwinACLFixture(t, [2]uint32{aceAllow, rightReadData}), false, false},
		{"file read allowed", darwinACLFixture(t, [2]uint32{aceAllow, rightReadData}), true, true},
		{"directory mutation allowed", darwinACLFixture(t, [2]uint32{aceAllow, rightWriteData | rightDeleteChild}), false, true},
		{"inherit only ignored", darwinACLFixture(t, [2]uint32{aceAllow | aceInheritOnly, rightGenericAll}), true, false},
		{"unknown entry rejected", darwinACLFixture(t, [2]uint32{3, 0}), true, true},
		{"truncated", []byte{1, 2, 3}, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDarwinACLBuffer(test.buffer, test.privateFile)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
