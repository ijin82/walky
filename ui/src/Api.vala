namespace Walky {
    /**
     * Thin D-Bus client for the daemon. All calls are asynchronous; the window
     * polls status and events once per second.
     */
    public class Api : GLib.Object {
        public const string BUS_NAME = "io.github.ijin.Walky1";
        public const string OBJECT_PATH = "/io/github/ijin/Walky";
        public const string INTERFACE = "io.github.ijin.Walky1";

        private GLib.DBusProxy? proxy = null;

        public signal void status_updated(GLib.Variant status);
        public signal void events_updated(GLib.Variant events);
        public signal void unavailable(string message);

        public Api () {
            try {
                proxy = new GLib.DBusProxy.for_bus_sync(
                    GLib.BusType.SESSION,
                    GLib.DBusProxyFlags.NONE,
                    null,
                    BUS_NAME,
                    OBJECT_PATH,
                    INTERFACE,
                    null
                );
            } catch (Error e) {
                warning("cannot create D-Bus proxy: %s", e.message);
            }
        }

        public void refresh () {
            fetch_status.begin();
            fetch_events.begin();
        }

        public void sync_now () {
            invoke.begin("SyncNow", new GLib.Variant("(b)", false));
        }

        public void pause (bool paused) {
            invoke.begin("Pause", new GLib.Variant("(b)", paused));
        }

        public void eject () {
            invoke.begin("Eject", null);
        }

        public void verify () {
            invoke.begin("Verify", new GLib.Variant("(b)", false));
        }

        private async void fetch_status () {
            if (proxy == null) {
                unavailable("Walky daemon is not running");
                return;
            }
            try {
                var reply = yield proxy.call("GetStatus", null,
                    GLib.DBusCallFlags.NONE, 2000, null);
                status_updated(reply.get_child_value(0));
            } catch (Error e) {
                unavailable(e.message);
            }
        }

        private async void fetch_events () {
            if (proxy == null) {
                return;
            }
            try {
                var reply = yield proxy.call("GetEvents", new GLib.Variant("(u)", 5u),
                    GLib.DBusCallFlags.NONE, 2000, null);
                events_updated(reply.get_child_value(0));
            } catch (Error e) {
                // The status handler reports daemon failures.
            }
        }

        private async void invoke (string method, GLib.Variant? args) {
            if (proxy == null) {
                unavailable("Walky daemon is not running");
                return;
            }
            try {
                yield proxy.call(method, args, GLib.DBusCallFlags.NONE, 30000, null);
            } catch (Error e) {
                unavailable(e.message);
            }
        }
    }
}
