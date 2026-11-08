package awake

import (
	"os"

	"github.com/godbus/dbus/v5"
)

// suspend is the portal's flag against suspending; idle would also keep the screen from locking.
const suspend uint32 = 4

// inhibit asks the desktop portal, which drops the request when castor's connection closes, even on a crash.
func inhibit() (func(), error) {
	// No session bus, as in a container, means no desktop that would idle to sleep.
	conn, err := dbus.Connect(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		return nil, err
	}
	var request dbus.ObjectPath
	portal := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	options := map[string]dbus.Variant{"reason": dbus.MakeVariant("Casting")}
	if err := portal.Call("org.freedesktop.portal.Inhibit.Inhibit", 0, "", suspend, options).Store(&request); err != nil {
		conn.Close()
		return nil, err
	}
	return func() {
		conn.Object("org.freedesktop.portal.Desktop", request).Call("org.freedesktop.portal.Request.Close", 0)
		conn.Close()
	}, nil
}
