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
        public signal void settings_updated(GLib.Variant settings);
        public signal void settings_saved();
        public signal void settings_failed(string message);
        public signal void drives_updated(GLib.Variant drives);
        public signal void store_ready(string repo_id);
        public signal void store_failed(string message);

        private bool daemon_start_attempted = false;

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

        public void fetch_settings () {
            fetch_settings_async.begin();
        }

        public void save_settings (string[] excludes, int daily, int weekly, int monthly,
                                   int trash_days, string conflict) {
            save_settings_async.begin(excludes, daily, weekly, monthly, trash_days, conflict);
        }

        public void fetch_drives () {
            fetch_drives_async.begin();
        }

        public void init_store (string mount_point, string repo_path) {
            init_store_async.begin(mount_point, repo_path, true);
        }

        public void use_store (string mount_point, string repo_path) {
            init_store_async.begin(mount_point, repo_path, false);
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
                var message = e.message;
                if (message.contains("ServiceUnknown") ||
                    message.contains("NameHasNoOwner") ||
                    message.contains("was not provided by any .service files")) {
                    unavailable("Walky daemon is not running");
                } else {
                    unavailable(message);
                }
                try_start_daemon();
            }
        }

        public void start_daemon () {
            daemon_start_attempted = false;
            try_start_daemon();
            GLib.Timeout.add_seconds(1, () => {
                refresh();
                return false;
            });
        }

        private async void fetch_settings_async () {
            if (proxy == null) {
                settings_failed("Walky daemon is not running");
                return;
            }
            try {
                var reply = yield proxy.call("GetSettings", null,
                    GLib.DBusCallFlags.NONE, 2000, null);
                settings_updated(reply.get_child_value(0));
            } catch (Error e) {
                settings_failed(e.message);
            }
        }

        private async void save_settings_async (string[] excludes, int daily, int weekly,
                                                int monthly, int trash_days, string conflict) {
            if (proxy == null) {
                settings_failed("Walky daemon is not running");
                return;
            }
            try {
                var args = new GLib.Variant("(asiiiis)", excludes, daily, weekly,
                    monthly, trash_days, conflict);
                yield proxy.call("SetSettings", args, GLib.DBusCallFlags.NONE, 5000, null);
                settings_saved();
            } catch (Error e) {
                settings_failed(e.message);
            }
        }

        // Start walky.service through the user systemd manager once. If the unit
        // is not installed, the error is ignored and the UI keeps polling.
        private void try_start_daemon () {
            if (daemon_start_attempted) {
                return;
            }
            daemon_start_attempted = true;
            try {
                var manager = new GLib.DBusProxy.for_bus_sync(
                    GLib.BusType.SESSION, GLib.DBusProxyFlags.NONE, null,
                    "org.freedesktop.systemd1", "/org/freedesktop/systemd1",
                    "org.freedesktop.systemd1.Manager", null);
                manager.call.begin("StartUnit",
                    new GLib.Variant("(ss)", "walky.service", "replace"),
                    GLib.DBusCallFlags.NONE, 5000, null, (obj, res) => {
                        try {
                            manager.call.end(res);
                        } catch (Error e) {
                            warning("cannot start walky.service: %s", e.message);
                        }
                    });
            } catch (Error e) {
                warning("systemd manager unavailable: %s", e.message);
            }
        }

        private async void fetch_drives_async () {
            if (proxy == null) {
                store_failed("Walky daemon is not running");
                return;
            }
            try {
                var reply = yield proxy.call("GetDrives", null,
                    GLib.DBusCallFlags.NONE, 3000, null);
                drives_updated(reply.get_child_value(0));
            } catch (Error e) {
                store_failed(e.message);
            }
        }

        private async void init_store_async (string mount_point, string repo_path, bool create) {
            if (proxy == null) {
                store_failed("Walky daemon is not running");
                return;
            }
            try {
                var method = create ? "InitStore" : "UseStore";
                var reply = yield proxy.call(method,
                    new GLib.Variant("(ss)", mount_point, repo_path),
                    GLib.DBusCallFlags.NONE, 15000, null);
                store_ready(reply.get_child_value(0).get_string());
            } catch (Error e) {
                store_failed(e.message);
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
