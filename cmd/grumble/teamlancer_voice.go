package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"mumble.info/grumble/pkg/mumbleproto"
	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
	tlvoice "mumble.info/grumble/pkg/teamlancer/voice"
)

func (server *Server) resolveAuthenticatedChannel(client *Client) (*Channel, error) {
	channel := server.RootChannel()
	if client.IsRegistered() {
		lastChannel := server.Channels[client.user.LastChannelId]
		if lastChannel != nil {
			channel = lastChannel
		}
	}

	if !runtimeConfig.TeamlancerMode || client.teamlancerIdentity == nil {
		return channel, nil
	}
	boardID := strings.TrimSpace(client.teamlancerIdentity.BoardID)
	if boardID == "" {
		return channel, nil
	}

	return server.resolveBoardVoiceChannel(client.teamlancerIdentity)
}

func (server *Server) resolveBoardVoiceChannel(identity *tlauth.UserIdentity) (*Channel, error) {
	if !tlvoice.CanJoinVoice(identity) {
		return nil, tlvoice.ErrJoinDenied
	}
	if server.teamlancerVoiceRooms == nil {
		return nil, errors.New("voice room manager not initialized")
	}

	boardID := strings.TrimSpace(identity.BoardID)
	if room, ok := server.teamlancerVoiceRooms.Find(boardID); ok {
		channel, exists := server.Channels[room.ChannelID]
		if exists {
			return channel, nil
		}
	}

	channel := server.AddChannel(tlvoice.ChannelName(boardID))
	channel.temporary = true
	server.RootChannel().AddChild(channel)

	server.broadcastProtoMessage(&mumbleproto.ChannelState{
		ChannelId: proto.Uint32(uint32(channel.Id)),
		Parent:    proto.Uint32(uint32(server.RootChannel().Id)),
		Name:      proto.String(channel.Name),
		Temporary: proto.Bool(true),
		Position:  proto.Int32(int32(channel.Position)),
	})

	return channel, nil
}

func (server *Server) joinBoardVoiceRoom(identity *tlauth.UserIdentity, channel *Channel) (*tlvoice.VoiceRoom, bool, error) {
	if identity == nil || channel == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(identity.BoardID) == "" {
		return nil, false, nil
	}
	return server.teamlancerVoiceRooms.Join(identity, channel.Id)
}

func (server *Server) leaveBoardVoiceRoom(client *Client) (*tlvoice.VoiceRoom, bool, bool) {
	if client == nil || client.teamlancerIdentity == nil || server.teamlancerVoiceRooms == nil {
		return nil, false, false
	}
	boardID := strings.TrimSpace(client.teamlancerIdentity.BoardID)
	userID := strings.TrimSpace(client.teamlancerIdentity.UserID)
	if boardID == "" || userID == "" {
		return nil, false, false
	}
	return server.teamlancerVoiceRooms.Leave(boardID, userID)
}

func (server *Server) boardVoiceIsolationApplies(identity *tlauth.UserIdentity) bool {
	return server.teamlancerPermissionEnforcementEnabled() && identity != nil && strings.TrimSpace(identity.BoardID) != ""
}

func (server *Server) findBoardVoiceRoomByChannel(channel *Channel) (*tlvoice.VoiceRoom, bool) {
	if channel == nil || server.teamlancerVoiceRooms == nil {
		return nil, false
	}
	return server.teamlancerVoiceRooms.FindByChannelID(channel.Id)
}

func (server *Server) canMoveBoardScopedClient(client *Client, channel *Channel) bool {
	if client == nil || channel == nil || !server.boardVoiceIsolationApplies(client.teamlancerIdentity) {
		return true
	}
	room, ok := server.findBoardVoiceRoomByChannel(channel)
	if !ok {
		return false
	}
	return tlvoice.CanMoveChannel(client.teamlancerIdentity, room)
}

func (server *Server) canReceiveFromClient(target *Client, sender *Client) bool {
	if target == nil || sender == nil {
		return false
	}
	if !server.boardVoiceIsolationApplies(target.teamlancerIdentity) && !server.boardVoiceIsolationApplies(sender.teamlancerIdentity) {
		return true
	}
	room, ok := server.findBoardVoiceRoomByChannel(sender.Channel)
	if !ok {
		return false
	}
	return tlvoice.CanReceiveFromRoom(target.teamlancerIdentity, room)
}

func (server *Server) canAccessVoiceTargetChannel(sender *Client, channel *Channel) bool {
	if sender == nil || channel == nil || !server.boardVoiceIsolationApplies(sender.teamlancerIdentity) {
		return true
	}
	room, ok := server.findBoardVoiceRoomByChannel(channel)
	if !ok {
		return false
	}
	return tlvoice.CanMoveChannel(sender.teamlancerIdentity, room)
}

func (server *Server) logVoiceChannelMoveDenied(client *Client, channel *Channel, reason string) {
	if client == nil || client.teamlancerIdentity == nil {
		return
	}
	fields := map[string]string{
		"user_id":           strings.TrimSpace(client.teamlancerIdentity.UserID),
		"board_id":          strings.TrimSpace(client.teamlancerIdentity.BoardID),
		"requested_channel": strconv.Itoa(channel.Id),
		"reason":            reason,
	}
	emitStructuredEvent(server.Logger, "warn", "voice_channel_move_denied", fields)
}

func (server *Server) logVoiceCrossBoardAccessDenied(identity *tlauth.UserIdentity, channel *Channel, reason string) {
	if identity == nil || channel == nil {
		return
	}
	fields := map[string]string{
		"user_id":           strings.TrimSpace(identity.UserID),
		"board_id":          strings.TrimSpace(identity.BoardID),
		"requested_channel": strconv.Itoa(channel.Id),
		"reason":            reason,
	}
	emitStructuredEvent(server.Logger, "warn", "voice_cross_board_access_denied", fields)
}

func (server *Server) logVoiceRoomEvent(event string, room *tlvoice.VoiceRoom, userID string) {
	if room == nil {
		return
	}
	fields := map[string]string{
		"room_id":  room.RoomID,
		"board_id": room.BoardID,
		"team_id":  room.TeamID,
		"user_id":  userID,
	}
	emitStructuredEvent(server.Logger, "info", event, fields)
}
