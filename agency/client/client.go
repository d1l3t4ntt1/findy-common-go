package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/findy-network/findy-agent-api/grpc/agency"
	"github.com/findy-network/findy-agent-api/grpc/ops"
	didexchange "github.com/findy-network/findy-agent/std/didexchange/invitation"
	"github.com/findy-network/findy-grpc/jwt"
	"github.com/findy-network/findy-grpc/rpc"
	"github.com/findy-network/findy-grpc/utils"
	"github.com/golang/glog"
	"github.com/lainio/err2"
	"google.golang.org/grpc"
)

type Conn struct {
	*grpc.ClientConn
	cfg *rpc.ClientCfg
}

type Pairwise struct {
	Conn
	ID    string
	Label string
}

func BuildClientConnBase(tlsPath, addr string, port int, opts []grpc.DialOption) *rpc.ClientCfg {
	cfg := &rpc.ClientCfg{
		PKI:  rpc.LoadPKI(tlsPath),
		JWT:  "",
		Addr: fmt.Sprintf("%s:%d", addr, port),
		Opts: opts,
	}
	return cfg
}

func TryAuthOpen(jwtToken string, conf *rpc.ClientCfg) (c Conn) {
	if conf == nil {
		panic(errors.New("conf cannot be nil"))
	}
	conf.JWT = jwtToken
	conn, err := rpc.ClientConn(*conf)
	err2.Check(err)
	return Conn{ClientConn: conn, cfg: conf}
}

func TryOpen(user string, conf *rpc.ClientCfg) (c Conn) {
	glog.V(3).Infof("client with user \"%s\"", user)
	return TryAuthOpen(jwt.BuildJWT(user), conf)
}

func OkStatus(s *agency.ProtocolState) bool {
	return s.State == agency.ProtocolState_OK
}

func (pw Pairwise) Issue(ctx context.Context, credDefID, attrsJSON string) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_ISSUE,
		Role:         agency.Protocol_INITIATOR,
		StartMsg: &agency.Protocol_CredDef{CredDef: &agency.Protocol_Issuing{
			CredDefId: credDefID,
			Attrs:     &agency.Protocol_Issuing_AttributesJson{AttributesJson: attrsJSON},
		}},
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (pw Pairwise) IssueWithAttrs(ctx context.Context, credDefID string, attrs *agency.Protocol_Attrs) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_ISSUE,
		Role:         agency.Protocol_INITIATOR,
		StartMsg: &agency.Protocol_CredDef{CredDef: &agency.Protocol_Issuing{
			CredDefId: credDefID,
			Attrs:     &agency.Protocol_Issuing_Attrs_{Attrs_: attrs},
		}},
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (pw *Pairwise) Connection(ctx context.Context, invitationJSON string) (connID string, ch chan *agency.ProtocolState, err error) {
	defer err2.Return(&err)

	// assert that invitation is OK, and we need to return the connection ID
	// because it's the task id as well
	var invitation didexchange.Invitation
	err2.Check(json.Unmarshal([]byte(invitationJSON), &invitation))

	protocol := &agency.Protocol{
		TypeId: agency.Protocol_CONNECT,
		Role:   agency.Protocol_INITIATOR,
		StartMsg: &agency.Protocol_ConnAttr{ConnAttr: &agency.Protocol_Connection{
			Label:          pw.Label,
			InvitationJson: invitationJSON,
		}},
	}
	ch, err = pw.Conn.doRun(ctx, protocol)
	err2.Check(err)
	connID = invitation.ID
	pw.ID = connID
	return connID, ch, err
}

func (pw Pairwise) Ping(ctx context.Context) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_TRUST_PING,
		Role:         agency.Protocol_INITIATOR,
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (pw Pairwise) BasicMessage(ctx context.Context, content string) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_BASIC_MESSAGE,
		Role:         agency.Protocol_INITIATOR,
		StartMsg:     &agency.Protocol_BasicMessage{BasicMessage: content},
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (pw Pairwise) ReqProof(ctx context.Context, proofAttrs string) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_PROOF,
		Role:         agency.Protocol_INITIATOR,
		StartMsg: &agency.Protocol_ProofReq{
			ProofReq: &agency.Protocol_ProofRequest{
				AttrFmt: &agency.Protocol_ProofRequest_AttributesJson{
					AttributesJson: proofAttrs}}},
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (pw Pairwise) ReqProofWithAttrs(ctx context.Context, proofAttrs *agency.Protocol_Proof) (ch chan *agency.ProtocolState, err error) {
	protocol := &agency.Protocol{
		ConnectionId: pw.ID,
		TypeId:       agency.Protocol_PROOF,
		Role:         agency.Protocol_INITIATOR,
		StartMsg: &agency.Protocol_ProofReq{
			ProofReq: &agency.Protocol_ProofRequest{
				AttrFmt: &agency.Protocol_ProofRequest_Attrs{
					Attrs: proofAttrs}}},
	}
	return pw.Conn.doRun(ctx, protocol)
}

func (conn Conn) Listen(ctx context.Context, protocol *agency.ClientID) (ch chan *agency.AgentStatus, err error) {
	defer err2.Return(&err)

	c := agency.NewAgentClient(conn)
	statusCh := make(chan *agency.AgentStatus)

	stream, err := c.Listen(ctx, protocol)
	err2.Check(err)
	glog.V(3).Infoln("successful start of listen id:", protocol.Id)
	go func() {
		defer err2.CatchTrace(func(err error) {
			glog.V(1).Infoln("WARNING: error when reading response:", err)
			close(statusCh)
		})
		for {
			status, err := stream.Recv()
			if err == io.EOF {
				glog.V(3).Infoln("status stream end")
				close(statusCh)
				break
			}
			err2.Check(err)
			statusCh <- status
		}
	}()
	return statusCh, nil
}

func (conn Conn) PSMHook(ctx context.Context) (ch chan *ops.AgencyStatus, err error) {
	defer err2.Return(&err)

	opsClient := ops.NewAgencyClient(conn)
	statusCh := make(chan *ops.AgencyStatus)

	stream, err := opsClient.PSMHook(ctx, &ops.DataHook{Id: utils.UUID()})
	err2.Check(err)
	glog.V(3).Infoln("successful start of listen PSM hook id:")
	go func() {
		defer err2.CatchTrace(func(err error) {
			glog.V(1).Infoln("WARNING: error when reading response:", err)
			close(statusCh)
		})
		for {
			status, err := stream.Recv()
			if err == io.EOF {
				glog.V(3).Infoln("status stream end")
				close(statusCh)
				break
			}
			err2.Check(err)
			statusCh <- status
		}
	}()
	return statusCh, nil
}

func (conn Conn) doRun(ctx context.Context, protocol *agency.Protocol) (ch chan *agency.ProtocolState, err error) {
	defer err2.Return(&err)

	c := agency.NewDIDCommClient(conn)
	statusCh := make(chan *agency.ProtocolState)

	stream, err := c.Run(ctx, protocol)
	err2.Check(err)
	glog.V(3).Infoln("successful start of:", protocol.TypeId)
	go func() {
		defer err2.CatchTrace(func(err error) {
			glog.V(3).Infoln("err when reading response", err)
			close(statusCh)
		})
		for {
			status, err := stream.Recv()
			if err == io.EOF {
				glog.V(3).Infoln("status stream end")
				close(statusCh)
				break
			}
			err2.Check(err)
			statusCh <- status
		}
	}()
	return statusCh, nil
}

func (conn Conn) DoStart(ctx context.Context, protocol *agency.Protocol, cOpts ...grpc.CallOption) (pid *agency.ProtocolID, err error) {
	defer err2.Return(&err)

	c := agency.NewDIDCommClient(conn)
	pid, err = c.Start(ctx, protocol, cOpts...)
	err2.Check(err)

	glog.V(3).Infoln("successful start of:", protocol.TypeId)
	return pid, nil
}
