//go:build windows

package tunnel

import (
	"fmt"
	"myvpn/internal/helper"
)

func Setup() error {
	return helper.SendCommand(helper.Command{
		Action: "create_tun",
		TUNIP:  "10.0.0.2/24",
	})
}

func Teardown() error {
	return helper.SendCommand(helper.Command{
		Action: "destroy_tun",
	})
}

func AddRoute(dest, gateway string) error {
	return helper.SendCommand(helper.Command{
		Action:  "add_route",
		Dest:    dest,
		Gateway: gateway,
		Mask:    "255.255.255.255",
	})
}

func RemoveRoute(dest string) error {
	return helper.SendCommand(helper.Command{
		Action: "remove_route",
		Dest:   dest,
	})
}
