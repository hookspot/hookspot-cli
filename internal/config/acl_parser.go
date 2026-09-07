package config

import (
	"encoding/binary"
	"errors"
)

const (
	attributeLengthOffset           = 0
	aclReferenceOffset              = 4
	aclReferenceLengthOffset        = 8
	attributeHeaderSize             = 12
	fileSecurityMagic               = 0x012cc16d
	aclCountOffset                  = 36
	aclFlagsOffset                  = 40
	fileSecurityHeaderSize          = 44
	aceSize                         = 24
	aceFlagsOffset                  = 16
	aceRightsOffset                 = 20
	maxACLEntries                   = 128
	noACLCount               uint32 = 0xffffffff

	aceKindMask    = 0xf
	aceAllow       = 1
	aceDeny        = 2
	aceInheritOnly = 1 << 8

	rightReadData                = 1 << 1
	rightWriteData               = 1 << 2
	rightDelete                  = 1 << 4
	rightAppendData              = 1 << 5
	rightDeleteChild             = 1 << 6
	rightWriteAttributes         = 1 << 8
	rightWriteExtendedAttributes = 1 << 10
	rightWriteSecurity           = 1 << 12
	rightChangeOwner             = 1 << 13
	rightGenericAll              = 1 << 21
	rightGenericWrite            = 1 << 23
	rightGenericRead             = 1 << 24
)

const mutationRights = rightWriteData | rightDelete | rightAppendData | rightDeleteChild |
	rightWriteAttributes | rightWriteExtendedAttributes | rightWriteSecurity |
	rightChangeOwner | rightGenericAll | rightGenericWrite

func validateDarwinACLBuffer(buffer []byte, privateFile bool) error {
	if len(buffer) < attributeHeaderSize {
		return errors.New("malformed ACL attribute header")
	}
	total := int(binary.LittleEndian.Uint32(buffer[attributeLengthOffset:aclReferenceOffset]))
	if total < attributeHeaderSize || total > len(buffer) {
		return errors.New("malformed ACL attribute length")
	}
	reference := int(int32(binary.LittleEndian.Uint32(buffer[aclReferenceOffset:aclReferenceLengthOffset])))
	length := int(binary.LittleEndian.Uint32(buffer[aclReferenceLengthOffset:attributeHeaderSize]))
	start := aclReferenceOffset + reference
	if length == 0 {
		if start < attributeHeaderSize || start > total {
			return errors.New("malformed empty ACL reference")
		}
		return nil
	}
	if start < attributeHeaderSize || start > total || length > total-start || length < fileSecurityHeaderSize {
		return errors.New("malformed ACL security bounds")
	}
	security := buffer[start : start+length]
	if binary.LittleEndian.Uint32(security[:4]) != fileSecurityMagic {
		return errors.New("unsupported ACL security format")
	}
	count := binary.LittleEndian.Uint32(security[aclCountOffset:aclFlagsOffset])
	_ = binary.LittleEndian.Uint32(security[aclFlagsOffset:fileSecurityHeaderSize])
	if count == noACLCount {
		return nil
	}
	if count > maxACLEntries || int(count)*aceSize > len(security)-fileSecurityHeaderSize {
		return errors.New("malformed ACL entry count")
	}
	dangerous := uint32(mutationRights)
	if privateFile {
		dangerous |= rightReadData | rightGenericRead
	}
	for index := 0; index < int(count); index++ {
		start := fileSecurityHeaderSize + index*aceSize
		ace := security[start : start+aceSize]
		flags := binary.LittleEndian.Uint32(ace[aceFlagsOffset:aceRightsOffset])
		rights := binary.LittleEndian.Uint32(ace[aceRightsOffset:aceSize])
		if flags&aceInheritOnly != 0 || flags&aceKindMask == aceDeny {
			continue
		}
		if flags&aceKindMask != aceAllow {
			return errors.New("unsupported ACL entry kind")
		}
		if rights&dangerous != 0 {
			return errors.New("ACL contains a relevant allow entry")
		}
	}
	return nil
}
