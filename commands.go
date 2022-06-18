// mautrix-discord - A Matrix-Discord puppeting bridge.
// Copyright (C) 2022 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridge/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-discord/remoteauth"
)

type WrappedCommandEvent struct {
	*commands.Event
	Bridge *DiscordBridge
	User   *User
	Portal *Portal
}

func (br *DiscordBridge) RegisterCommands() {
	proc := br.CommandProcessor.(*commands.Processor)
	proc.AddHandlers(
		cmdLogin,
		cmdLogout,
		cmdReconnect,
		cmdDisconnect,
		cmdGuilds,
		cmdDeleteAllPortals,
	)
}

func wrapCommand(handler func(*WrappedCommandEvent)) func(*commands.Event) {
	return func(ce *commands.Event) {
		user := ce.User.(*User)
		var portal *Portal
		if ce.Portal != nil {
			portal = ce.Portal.(*Portal)
		}
		br := ce.Bridge.Child.(*DiscordBridge)
		handler(&WrappedCommandEvent{ce, br, user, portal})
	}
}

var cmdLogin = &commands.FullHandler{
	Func: wrapCommand(fnLogin),
	Name: "login",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Link the bridge to your Discord account by scanning a QR code.",
	},
}

func fnLogin(ce *WrappedCommandEvent) {
	if ce.User.IsLoggedIn() {
		ce.Reply("You're already logged in")
		return
	}

	client, err := remoteauth.New()
	if err != nil {
		ce.Reply("Failed to prepare login: %v", err)
		return
	}

	qrChan := make(chan string)
	doneChan := make(chan struct{})

	var qrCodeEvent id.EventID

	go func() {
		code := <-qrChan
		resp := sendQRCode(ce, code)
		qrCodeEvent = resp
	}()

	ctx := context.Background()

	if err = client.Dial(ctx, qrChan, doneChan); err != nil {
		close(qrChan)
		close(doneChan)
		ce.Reply("Error connecting to login websocket: %v", err)
		return
	}

	<-doneChan

	if qrCodeEvent != "" {
		_, _ = ce.MainIntent().RedactEvent(ce.RoomID, qrCodeEvent)
	}

	user, err := client.Result()
	if err != nil || len(user.Token) == 0 {
		ce.Reply("Error logging in: %v", err)
	} else if err = ce.User.Login(user.Token); err != nil {
		ce.Reply("Error connecting after login: %v", err)
	}
	ce.User.Lock()
	ce.User.DiscordID = user.UserID
	ce.User.Update()
	ce.User.Unlock()
	ce.Reply("Successfully logged in as %s#%s", user.Username, user.Discriminator)
}

func sendQRCode(ce *WrappedCommandEvent, code string) id.EventID {
	url, ok := uploadQRCode(ce, code)
	if !ok {
		return ""
	}

	content := event.MessageEventContent{
		MsgType: event.MsgImage,
		Body:    code,
		URL:     url.CUString(),
	}

	resp, err := ce.Bot.SendMessageEvent(ce.RoomID, event.EventMessage, &content)
	if err != nil {
		ce.Log.Errorfln("Failed to send QR code: %v", err)
		return ""
	}

	return resp.EventID
}

func uploadQRCode(ce *WrappedCommandEvent, code string) (id.ContentURI, bool) {
	qrCode, err := qrcode.Encode(code, qrcode.Low, 256)
	if err != nil {
		ce.Log.Errorln("Failed to encode QR code:", err)
		ce.Reply("Failed to encode QR code: %v", err)
		return id.ContentURI{}, false
	}

	resp, err := ce.Bot.UploadBytes(qrCode, "image/png")
	if err != nil {
		ce.Log.Errorln("Failed to upload QR code:", err)
		ce.Reply("Failed to upload QR code: %v", err)
		return id.ContentURI{}, false
	}

	return resp.ContentURI, true
}

var cmdLogout = &commands.FullHandler{
	Func: wrapCommand(fnLogout),
	Name: "logout",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Unlink the bridge from your Discord account.",
	},
	RequiresLogin: true,
}

func fnLogout(ce *WrappedCommandEvent) {
	err := ce.User.Logout()
	if err != nil {
		ce.Reply("Error logging out: %v", err)
	} else {
		ce.Reply("Logged out successfully.")
	}
}

var cmdDisconnect = &commands.FullHandler{
	Func: wrapCommand(fnDisconnect),
	Name: "disconnect",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Disconnect from Discord (without logging out)",
	},
	RequiresLogin: true,
}

func fnDisconnect(ce *WrappedCommandEvent) {
	if !ce.User.Connected() {
		ce.Reply("You're already not connected")
	} else if err := ce.User.Disconnect(); err != nil {
		ce.Reply("Error while disconnecting: %v", err)
	} else {
		ce.Reply("Successfully disconnected")
	}
}

var cmdReconnect = &commands.FullHandler{
	Func:    wrapCommand(fnReconnect),
	Name:    "reconnect",
	Aliases: []string{"connect"},
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Reconnect to Discord after disconnecting",
	},
	RequiresLogin: true,
}

func fnReconnect(ce *WrappedCommandEvent) {
	if ce.User.Connected() {
		ce.Reply("You're already connected")
	} else if err := ce.User.Connect(); err != nil {
		ce.Reply("Error while reconnecting: %v", err)
	} else {
		ce.Reply("Successfully reconnected")
	}
}

var cmdGuilds = &commands.FullHandler{
	Func:    wrapCommand(fnGuilds),
	Name:    "guilds",
	Aliases: []string{"servers", "guild", "server"},
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionUnclassified,
		Description: "Guild bridging management",
		Args:        "<status/bridge/unbridge> [_guild ID_] [--entire]",
	},
	RequiresLogin: true,
}

func fnGuilds(ce *WrappedCommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage**: `$cmdprefix guilds <status/bridge/unbridge> [guild ID] [--entire]`")
		return
	}
	subcommand := strings.ToLower(ce.Args[0])
	ce.Args = ce.Args[1:]
	switch subcommand {
	case "status":
		fnListGuilds(ce)
	case "bridge":
		fnBridgeGuild(ce)
	case "unbridge":
		fnUnbridgeGuild(ce)
	}
}

func fnListGuilds(ce *WrappedCommandEvent) {
	var output strings.Builder
	for _, userGuild := range ce.User.GetPortals() {
		guild := ce.Bridge.GetGuildByID(userGuild.DiscordID, false)
		if guild == nil {
			continue
		}
		status := "not bridged"
		if guild.MXID != "" {
			status = "bridged"
		}
		_, _ = fmt.Fprintf(&output, "* %s (`%s`) - %s\n", guild.Name, guild.ID, status)
	}
	if output.Len() == 0 {
		ce.Reply("No guilds found")
	} else {
		ce.Reply("List of guilds:\n\n%s", output.String())
	}
}

func fnBridgeGuild(ce *WrappedCommandEvent) {
	if len(ce.Args) == 0 || len(ce.Args) > 2 {
		ce.Reply("**Usage**: `$cmdprefix guilds bridge <guild ID> [--entire]")
	} else if err := ce.User.bridgeGuild(ce.Args[0], len(ce.Args) == 2 && strings.ToLower(ce.Args[1]) == "--entire"); err != nil {
		ce.Reply("Error bridging guild: %v", err)
	} else {
		ce.Reply("Successfully bridged guild")
	}
}
func fnUnbridgeGuild(ce *WrappedCommandEvent) {
	if len(ce.Args) != 1 {
		ce.Reply("**Usage**: `$cmdprefix guilds unbridge <guild ID>")
	} else if err := ce.User.unbridgeGuild(ce.Args[0]); err != nil {
		ce.Reply("Error unbridging guild: %v", err)
	} else {
		ce.Reply("Successfully unbridged guild")
	}
}

var cmdDeleteAllPortals = &commands.FullHandler{
	Func: wrapCommand(fnDeleteAllPortals),
	Name: "delete-all-portals",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionUnclassified,
		Description: "Delete all portals.",
	},
	RequiresAdmin: true,
}

func fnDeleteAllPortals(ce *WrappedCommandEvent) {
	portals := ce.Bridge.GetAllPortals()
	if len(portals) == 0 {
		ce.Reply("Didn't find any portals")
		return
	}

	leave := func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, _ = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
				Reason: "Deleting portal",
				UserID: ce.User.MXID,
			})
		}
	}
	customPuppet := ce.Bridge.GetPuppetByCustomMXID(ce.User.MXID)
	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		intent := customPuppet.CustomIntent()
		leave = func(portal *Portal) {
			if len(portal.MXID) > 0 {
				_, _ = intent.LeaveRoom(portal.MXID)
				_, _ = intent.ForgetRoom(portal.MXID)
			}
		}
	}
	ce.Reply("Found %d portals, deleting...", len(portals))
	for _, portal := range portals {
		portal.Delete()
		leave(portal)
	}
	ce.Reply("Finished deleting portal info. Now cleaning up rooms in background.")

	go func() {
		for _, portal := range portals {
			portal.cleanup(false)
		}
		ce.Reply("Finished background cleanup of deleted portal rooms.")
	}()
}
