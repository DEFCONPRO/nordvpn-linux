package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/netip"

	"github.com/NordSecurity/nordvpn-linux/core/mesh"
	"github.com/NordSecurity/nordvpn-linux/request"

	"github.com/google/uuid"
)

const (
	// urlMeshRegister is used to register a single mesh machine/device.
	urlMeshRegister = "/v1/meshnet/machines"
	// urlMeshMachines is used to interact with a single mesh machine/device.
	urlMeshMachines = "/v1/meshnet/machines/%s"
	// urlMeshMachinesPeers is used to update peer e.g. if other peer can route through this peer/machine
	urlMeshMachinesPeers = "/v1/meshnet/machines/%s/peers/%s"
	// urlMeshMap is used to refresh libtelio.
	urlMeshMap = urlMeshMachines + "/map"
	// urlMeshPeers is used to interact with one's peers in the mesh network.
	urlMeshPeers = urlMeshMachines + "/peers"
	// urlMeshUnpair is used to unpair the invited peers.
	urlMeshUnpair = urlMeshMachines + "/peers/%s"
	// urlInvitationSend is used to invite other users to mesh network.
	urlInvitationSend = urlMeshMachines + "/invitations"
	// urlSentInvitationsList is used to view sent invitations.
	urlSentInvitationsList = urlInvitationSend + "/sent"
	// urlReceivedInvitationsList is used to view received invitations.
	urlReceivedInvitationsList = urlInvitationSend + "/received"
	// urlAcceptInvitation is used to accept an invitation.
	urlAcceptInvitation = urlInvitationSend + "/%s/accept"
	// urlRejectInvitation is used to reject an invitation.
	urlRejectInvitation = urlInvitationSend + "/%s/reject"
	// urlRevokeInvitation is used to revoke an invitation.
	urlRevokeInvitation = urlInvitationSend + "/%s"
	// urlNotifyFileTransfer is used to notify another peer about an incoming notification
	urlNotifyFileTransfer = urlMeshMachines + "/notifications/file-transfer"
)

var (
	// ErrPublicKeyNotProvided is returned when peer does not have a public key set.
	ErrPublicKeyNotProvided = errors.New("public key not provided")
	// ErrPeerOSNotProvided is returned when peer does not have os name or os version set.
	ErrPeerOSNotProvided = errors.New("os not provided")
	// ErrPeerEndpointsNotProvided is returned when peer has on endpoints.
	ErrPeerEndpointsNotProvided = errors.New("endpoints not provided")
)

func peersResponseToMachinePeers(rawPeers []mesh.MachinePeerResponse) []mesh.MachinePeer {
	peers := make([]mesh.MachinePeer, 0, len(rawPeers))
	for _, p := range rawPeers {
		var addr netip.Addr
		if len(p.Addresses) > 0 {
			addr = p.Addresses[0]
		}

		peers = append(peers, mesh.MachinePeer{

			ID:       p.ID,
			Hostname: p.Hostname,
			OS: mesh.OperatingSystem{
				Name:   p.OS,
				Distro: p.Distro,
			},
			PublicKey:                 p.PublicKey,
			Endpoints:                 p.Endpoints,
			Address:                   addr,
			Email:                     p.Email,
			IsLocal:                   p.IsLocal,
			DoesPeerAllowRouting:      p.DoesPeerAllowRouting,
			DoesPeerAllowInbound:      p.DoesPeerAllowInbound,
			DoesPeerAllowLocalNetwork: p.DoesPeerAllowLocalNetwork,
			DoesPeerAllowFileshare:    p.DoesPeerAllowFileshare,
			DoesPeerSupportRouting:    p.DoesPeerSupportRouting,
			DoIAllowRouting:           p.DoIAllowRouting,
			DoIAllowInbound:           p.DoIAllowInbound,
			DoIAllowLocalNetwork:      p.DoIAllowLocalNetwork,
			DoIAllowFileshare:         p.DoIAllowFileshare,
			AlwaysAcceptFiles:         p.AlwaysAcceptFiles,
		})
	}

	return peers
}

// Register peer to the mesh network.
func (api *DefaultAPI) Register(token string, peer mesh.Machine) (*mesh.Machine, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	if peer.PublicKey == "" {
		return nil, ErrPublicKeyNotProvided
	}

	if peer.OS.Name == "" || peer.OS.Distro == "" {
		return nil, ErrPeerOSNotProvided
	}

	data, err := json.Marshal(mesh.MachineCreateRequest{
		PublicKey:       peer.PublicKey,
		HardwareID:      peer.HardwareID,
		OS:              peer.OS.Name,
		Distro:          peer.OS.Distro,
		Endpoints:       peer.Endpoints,
		SupportsRouting: peer.SupportsRouting,
	})
	if err != nil {
		return nil, err
	}
	req, err := request.NewRequestWithBearerToken(
		http.MethodPost,
		api.agent,
		api.baseURL,
		urlMeshRegister,
		"application/json",
		"",
		"",
		bytes.NewBuffer(data),
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	var raw mesh.MachineCreateResponse
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(body, &raw)
	if err != nil {
		return nil, err
	}

	if len(raw.Addresses) < 1 {
		return nil, errors.New("invalid response")
	}

	var addr netip.Addr
	if len(raw.Addresses) > 0 {
		addr = raw.Addresses[0]
	}

	return &mesh.Machine{
		ID:        raw.Identifier,
		Hostname:  raw.Hostname,
		OS:        peer.OS,
		PublicKey: peer.PublicKey,
		Endpoints: raw.Endpoints,
		Address:   addr,
	}, nil
}

// Update publishes new endpoints.
func (api *DefaultAPI) Update(token string, id uuid.UUID, endpoints []netip.AddrPort) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	if len(endpoints) == 0 {
		return ErrPeerEndpointsNotProvided
	}

	data, err := json.Marshal(mesh.MachineUpdateRequest{
		Endpoints:       endpoints,
		SupportsRouting: true,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf(urlMeshMachines, id.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodPatch,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		bytes.NewBuffer(data),
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Configure interaction with a specific peer.
func (api *DefaultAPI) Configure(
	token string,
	id uuid.UUID,
	peerID uuid.UUID,
	doIAllowInbound bool,
	doIAllowRouting bool,
	doIAllowLocalNetwork bool,
	doIAllowFileshare bool,
	alwaysAcceptfiles bool,
) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	data, err := json.Marshal(mesh.PeerUpdateRequest{
		DoIAllowInbound:      doIAllowInbound,
		DoIAllowRouting:      doIAllowRouting,
		DoIAllowLocalNetwork: doIAllowLocalNetwork,
		DoIAllowFileshare:    doIAllowFileshare,
		AllwaysAcceptFiles:   alwaysAcceptfiles,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf(urlMeshMachinesPeers, id.String(), peerID.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodPatch,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		bytes.NewBuffer(data),
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Unregister peer from the mesh network.
func (api *DefaultAPI) Unregister(token string, self uuid.UUID) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlMeshMachines, self.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodDelete,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return ExtractError(resp)
}

func peersResponseToLocalPeers(rawPeers []mesh.MachinePeerResponse) []mesh.Machine {
	peers := make([]mesh.Machine, 0, len(rawPeers))

	for _, p := range rawPeers {
		var addr netip.Addr
		if len(p.Addresses) > 0 {
			addr = p.Addresses[0]
		}

		peers = append(peers, mesh.Machine{
			ID:       p.ID,
			Hostname: p.Hostname,
			OS: mesh.OperatingSystem{
				Name: p.OS, Distro: p.Distro,
			},
			PublicKey: p.PublicKey,
			Endpoints: p.Endpoints,
			Address:   addr,
		})
	}

	return peers
}

// Local peer list.
func (api *DefaultAPI) Local(token string) (mesh.Machines, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	req, err := request.NewRequestWithBearerToken(
		http.MethodGet,
		api.agent,
		api.baseURL,
		urlMeshMachines,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawPeers []mesh.MachinePeerResponse
	err = json.Unmarshal(body, &rawPeers)
	if err != nil {
		return nil, err
	}

	peers := peersResponseToLocalPeers(rawPeers)

	return peers, nil
}

func (api *DefaultAPI) Map(token string, self uuid.UUID) (*mesh.MachineMap, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlMeshMap, self.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodGet,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var raw mesh.MachineMapResponse
	err = json.Unmarshal(body, &raw)
	if err != nil {
		return nil, err
	}

	peers := peersResponseToMachinePeers(raw.Peers)

	var addr netip.Addr
	if len(raw.Addresses) > 0 {
		addr = raw.Addresses[0]
	}

	return &mesh.MachineMap{
		Machine: mesh.Machine{
			ID:        raw.ID,
			Hostname:  raw.Hostname,
			PublicKey: raw.PublicKey,
			Endpoints: raw.Endpoints,
			Address:   addr,
		},
		Hosts: raw.DNS.Hosts,
		Peers: peers,
		Raw:   body,
	}, nil
}

// List peers in the mesh network for a given peer.
func (api *DefaultAPI) List(token string, self uuid.UUID) (mesh.MachinePeers, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlMeshPeers, self.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodGet,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawPeers []mesh.MachinePeerResponse
	err = json.Unmarshal(body, &rawPeers)
	if err != nil {
		return nil, err
	}

	peers := peersResponseToMachinePeers(rawPeers)

	return peers, nil
}

// Unpair a given peer.
func (api *DefaultAPI) Unpair(token string, self uuid.UUID, peer uuid.UUID) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlMeshUnpair, self.String(), peer.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodDelete,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Invite to mesh.
func (api *DefaultAPI) Invite(
	token string,
	self uuid.UUID,
	email string,
	doIAllowInbound bool,
	doIAllowRouting bool,
	doIAllowLocalNetwork bool,
	doIAllowFileshare bool,
) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	data, err := json.Marshal(&mesh.SendInvitationRequest{
		Email:             email,
		AllowInbound:      doIAllowInbound,
		AllowRouting:      doIAllowRouting,
		AllowLocalNetwork: doIAllowLocalNetwork,
		AllowFileshare:    doIAllowFileshare,
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf(urlInvitationSend, self.String())

	req, err := request.NewRequestWithBearerToken(
		http.MethodPost,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		bytes.NewBuffer(data),
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Received invitations from other users.
func (api *DefaultAPI) Received(token string, self uuid.UUID) (mesh.Invitations, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlReceivedInvitationsList, self.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodGet,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var invitations mesh.Invitations
	err = json.Unmarshal(body, &invitations)
	if err != nil {
		return nil, err
	}
	return invitations, nil
}

// Sent invitations to other users.
func (api *DefaultAPI) Sent(token string, self uuid.UUID) (mesh.Invitations, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlSentInvitationsList, self.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodGet,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return nil, err
	}

	resp, err := api.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := ExtractError(resp); err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var invitations mesh.Invitations
	err = json.Unmarshal(body, &invitations)
	if err != nil {
		return nil, err
	}
	return invitations, nil
}

// Accept invitation.
func (api *DefaultAPI) Accept(
	token string,
	self uuid.UUID,
	invitation uuid.UUID,
	doIAllowInbound bool,
	doIAllowRouting bool,
	doIAllowLocalNetwork bool,
	doIAllowFileshare bool,
) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	data, err := json.Marshal(&mesh.AcceptInvitationRequest{
		AllowInbound:      doIAllowInbound,
		AllowRouting:      doIAllowRouting,
		AllowLocalNetwork: doIAllowLocalNetwork,
		AllowFileshare:    doIAllowFileshare,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf(urlAcceptInvitation, self.String(), invitation.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodPost,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		bytes.NewReader(data),
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Reject invitation.
func (api *DefaultAPI) Reject(token string, self uuid.UUID, invitation uuid.UUID) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlRejectInvitation, self.String(), invitation.String())
	req, err := request.NewRequestWithBearerToken(http.MethodPost, api.agent, api.baseURL, url, "application/json", "", "", nil, token)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Revoke invitation.
func (api *DefaultAPI) Revoke(token string, self uuid.UUID, invitation uuid.UUID) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlRevokeInvitation, self.String(), invitation.String())
	req, err := request.NewRequestWithBearerToken(
		http.MethodDelete,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		nil,
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}

// Notify peer about a new incoming transfer
func (api *DefaultAPI) NotifyNewTransfer(
	token string,
	self uuid.UUID,
	peer uuid.UUID,
	fileName string,
	fileCount int,
) error {
	api.mu.Lock()
	defer api.mu.Unlock()

	url := fmt.Sprintf(urlNotifyFileTransfer, self.String())

	dataUnmarshaled := mesh.NotificationNewTransactionRequest{
		ReceiverMachineIdentifier: peer.String(),
		FileCount:                 fileCount,
	}
	dataUnmarshaled.FileName = fileName // We must not log filenames, so setting it after log
	data, err := json.Marshal(dataUnmarshaled)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := request.NewRequestWithBearerToken(
		http.MethodPost,
		api.agent,
		api.baseURL,
		url,
		"application/json",
		"",
		"",
		bytes.NewReader(data),
		token,
	)
	if err != nil {
		return err
	}

	resp, err := api.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ExtractError(resp)
}
