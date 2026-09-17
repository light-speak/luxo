package rpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// ProtocolVersion identifies the complete internal RPC contract, independently
// of the product version and public SDK wire format. Peers must match exactly.
const ProtocolVersion uint32 = 3

const handshakeTimeout = 5 * time.Second
const protocolMagic = "LUXORPC\x00"

func protocolPreface() [12]byte {
	var preface [12]byte
	copy(preface[:8], protocolMagic)
	binary.BigEndian.PutUint32(preface[8:], ProtocolVersion)
	return preface
}

// initiateHandshake runs only for a newly dialed connection, never on pool hits.
// Cancellation closes the connection instead of racing a deadline reset.
func initiateHandshake(ctx context.Context, conn net.Conn, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	var stop func() bool
	if ctx.Done() != nil {
		stop = context.AfterFunc(ctx, func() { conn.Close() })
	}
	err := exchangePreface(conn)
	if stop != nil {
		stop()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return conn.SetDeadline(time.Time{})
}

func exchangePreface(conn net.Conn) error {
	preface := protocolPreface()
	if err := writePreface(conn, &preface); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, preface[:]); err != nil {
		return err
	}
	return validatePreface(preface)
}

func acceptHandshake(conn net.Conn) error {
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return err
	}
	var preface [12]byte
	if _, err := io.ReadFull(conn, preface[:]); err != nil {
		return err
	}
	// Only recognized RPC peers receive the local version. No request is read
	// before both ends have identified the same complete protocol contract.
	if string(preface[:8]) != protocolMagic {
		return fmt.Errorf("rpc: invalid protocol preface")
	}
	local := protocolPreface()
	if err := writePreface(conn, &local); err != nil {
		return err
	}
	if err := validatePreface(preface); err != nil {
		return err
	}
	return conn.SetDeadline(time.Time{})
}

func validatePreface(preface [12]byte) error {
	if string(preface[:8]) != protocolMagic {
		return fmt.Errorf("rpc: invalid protocol preface")
	}
	version := binary.BigEndian.Uint32(preface[8:])
	if version != ProtocolVersion {
		return fmt.Errorf("rpc: incompatible protocol version: peer=%d local=%d", version, ProtocolVersion)
	}
	return nil
}

func writePreface(conn net.Conn, preface *[12]byte) error {
	n, err := conn.Write(preface[:])
	if err == nil && n != len(preface) {
		return io.ErrShortWrite
	}
	return err
}
