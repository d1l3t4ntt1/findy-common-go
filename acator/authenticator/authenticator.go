package authenticator

import (
	"encoding/binary"

	"github.com/duo-labs/webauthn/protocol"
	"github.com/lainio/err2"
	"github.com/lainio/err2/assert"
)

// AttestationObject is WebAuthn way to present Attestations:
// https://www.w3.org/TR/webauthn/#sctn-attestation
// https://www.w3.org/TR/webauthn/#attestation-object
type AttestationObject struct {
	// The byteform version of the authenticator data, used in part for signature validation
	RawAuthData []byte `json:"authData"`
	// The format of the Attestation data.
	Format string `json:"fmt"`
	// The attestation statement data sent back if attestation is requested.
	AttStatement map[string]interface{} `json:"attStmt,omitempty"`
}

// TryMarshalData is MarshalData convenience wrapper
func TryMarshalData(data *protocol.AuthenticatorData) []byte {
	b, err := MarshalData(data)
	err2.Check(err)
	return b
}

// MarshalData marshals authenticator data to byte format specified in:
// https://www.w3.org/TR/webauthn/#sctn-authenticator-data
func MarshalData(ad *protocol.AuthenticatorData) (out []byte, err error) {
	defer err2.Annotate("marshal authenticator data", &err)

	assert.D.EqualInt(len(ad.RPIDHash), 32, "wrong RPIDHash length")

	out = make([]byte, 32+1+4, 37+lenAttestedCredentialData(ad)+10)
	copy(out, ad.RPIDHash)
	out[32] = byte(ad.Flags)
	binary.BigEndian.PutUint32(out[33:], ad.Counter)

	if ad.Flags.HasAttestedCredentialData() {
		out = marshalAttestedCredentialData(out, ad)
	}
	return out, nil
}

func marshalAttestedCredentialData(outData []byte, data *protocol.AuthenticatorData) []byte {
	assert.D.EqualInt(len(data.AttData.AAGUID), 16, "wrong AAGUID length")
	assert.D.NotEmpty(data.AttData.CredentialID, "empty credential id")
	assert.D.NotEmpty(data.AttData.CredentialPublicKey, "empty credential public key")

	outData = append(outData, data.AttData.AAGUID[:]...)

	idLength := uint16(len(data.AttData.CredentialID))
	outData = outData[:55]
	binary.BigEndian.PutUint16(outData[53:], idLength)

	outData = append(outData, data.AttData.CredentialID[:]...)

	outData = append(outData, data.AttData.CredentialPublicKey[:]...)

	return outData
}

func lenAttestedCredentialData(data *protocol.AuthenticatorData) int {
	l := len(data.AttData.AAGUID) +
		len(data.AttData.CredentialID) +
		len(data.AttData.CredentialPublicKey)
	return l
}
