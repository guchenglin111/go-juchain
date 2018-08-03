// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package protocol

import "github.com/juchain/go-juchain/common"

var (
	// Official short name of the protocol used during capability negotiation.
	DPOSProtocolName = "dpos"

	// Supported versions of the protocol (first is primary).
	DPOSProtocolVersions = []uint{1}

	// Number of implemented message corresponding to different protocol versions.
	DPOSProtocolLengths = []uint64{1}
)


// dpos protocol message codes
const (
	DPOSProtocolMaxMsgSize = 10 * 1024 // Maximum cap on the size of a protocol message

	// Protocol messages belonging to dpos/10
	SYNC_BIGPERIOD_REQUEST      = 0xa1
	SYNC_BIGPERIOD_RESPONSE     = 0xa2
	CONFIRMED_BLOCK_SYNC        = 0xa3

	DPOSMSG_SUCCESS = iota
	DPOSErrMsgTooLarge
	DPOSErrDecode
	DPOSErrInvalidMsgCode
	DPOSErrProtocolVersionMismatch
	DPOSErrNoStatusMsg
	DPOSErroPACKAGE_VERIFY_FAILURE
	DPOSErroPACKAGE_FAILURE
	DPOSErroPACKAGE_NOTSYNC
	DPOSErroPACKAGE_EMPTY
	DPOSErroVOTE_VERIFY_FAILURE
	DPOSErroCandidateFull
	DPOSErroDelegatorSign

	// election node
	STATE_LOOKING  = 0xb0
	STATE_CONFIRMED = 0xb1
	STATE_MISMATCHED = 0xb2
)

type DPOSErrCode int

func (e DPOSErrCode) String() string {
	return errorToString[int(e)]
}

// XXX change once legacy code is out
var DPOSerrorToString = map[int]string{
	DPOSErrMsgTooLarge:             "Message too long",
	DPOSErrDecode:                  "Invalid message",
	DPOSErrInvalidMsgCode:          "Invalid message code",
	DPOSErrProtocolVersionMismatch: "Protocol version mismatch",
	DPOSErrNoStatusMsg:             "No status message",
	DPOSErroPACKAGE_VERIFY_FAILURE: "Packaging node Id does not match",
	DPOSErroPACKAGE_FAILURE:        "Failed to package the block",
	DPOSErroPACKAGE_NOTSYNC:        "Failed to package block due to blocks syncing is not completed yet",
	DPOSErroPACKAGE_EMPTY:          "Packaging block is skipped due to there was no transaction found at the remote peer",
	DPOSErroVOTE_VERIFY_FAILURE:    "VotePresidentRequest is invalid",
	DPOSErroDelegatorSign:          "Delegators' signature is incorrect",
}

//
type SyncBigPeriodRequest struct {
	DelegatedTable     []string; // all 31 nodes id
	DelegatedTableSign common.Hash;
	NodeId             []byte
}

//
type SyncBigPeriodResponse struct {
	DelegatedTable     []string; // all 31 nodes id
	DelegatedTableSign common.Hash;
	State              uint8
	nodeId             []byte
}

//
type PackageRequest struct {
	Round         uint64
	PresidentId   string
}

type PackageResponse struct {
	Round         uint64
	PresidentId   string
	NewBlockHeader common.Hash
	Code          uint8
}

type RegisterCandidateRequest struct {
	CandidateId   []byte
}

type RegisterCandidateResponse struct {
	Candidates    []string
	CandidateId   []byte
	Code          uint8
}

type ConfirmedSyncMessage struct {
	Rounds        []uint64
	CandidateId   []byte
}
