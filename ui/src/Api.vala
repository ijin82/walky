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
        public signal void recovery_preview(int push, int pull, int conflicts, int deletes, int total);
        public signal void recovery_started();
        public signal void recovery_failed(string message);

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

        public signal void sync_finished();
        public signal void sync_failed(string message);

        public void sync_now () {
            sync_now_async.begin();
        }

        public void pause (bool paused) {
            invoke.begin("Pause", new GLib.Variant("(b)", paused));
        }

        public void eject () {
            invoke.begin("Eject", null);
        }

        public signal void verify_finished(string summary);
        public signal void verify_failed(string message);
        public signal void repo_path_changed(string repo_path);
        public signal void repo_path_failed(string message);
        public signal void snapshots_updated(GLib.Variant snapshots);
        public signal void snapshot_files_updated(GLib.Variant files);
        public signal void restore_finished(string restored_path);
        public signal void restore_failed(string message);

        public void verify () {
            verify_async.begin();
        }

        public void set_repo_path (string repo_path) {
            set_repo_path_async.begin(repo_path);
        }

        public void fetch_snapshots () {
            fetch_snapshots_async.begin();
        }

        public void fetch_snapshot_files (int64 gen, string query, uint32 limit) {
            fetch_snapshot_files_async.begin(gen, query, limit);
        }

        public void restore_file (int64 gen, string rel_path, string dest_dir) {
            restore_file_async.begin(gen, rel_path, dest_dir);
        }

        public void fetch_settings () {
            fetch_settings_async.begin();
        }

        public void save_settings (string[] excludes, int daily, int weekly, int monthly,
                                   int trash_days, string conflict, int poll_seconds, bool notify_on_sync) {
            save_settings_async.begin(excludes, daily, weekly, monthly, trash_days, conflict, poll_seconds, notify_on_sync);
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

        public void preview_recovery () {
            preview_recovery_async.begin();
        }

        public void start_recovery () {
            start_recovery_async.begin();
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
                                                int monthly, int trash_days, string conflict,
                                                int poll_seconds, bool notify_on_sync) {
            if (proxy == null) {
                settings_failed("Walky daemon is not running");
                return;
            }
            try {
                var args = new GLib.Variant("(asiiiisib)", excludes, daily, weekly,
                    monthly, trash_days, conflict, poll_seconds, notify_on_sync);
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

        private async void sync_now_async () {
            if (proxy == null) {
                sync_failed("Daemon unavailable");
                return;
            }
            try {
                yield proxy.call(
                    "SyncNow",
                    new GLib.Variant("(b)", true),
                    GLib.DBusCallFlags.NONE,
                    -1,
                    null
                );
                sync_finished();
                refresh();
            } catch (Error e) {
                sync_failed(e.message);
            }
        }

        private async void fetch_snapshots_async () {
            if (proxy == null) {
                return;
            }
            try {
                var reply = yield proxy.call(
                    "ListSnapshots", null, GLib.DBusCallFlags.NONE, -1, null);
                snapshots_updated(reply.get_child_value(0));
            } catch (Error e) {
                // No drive attached or daemon unavailable.
            }
        }

        private async void fetch_snapshot_files_async (int64 gen, string query, uint32 limit) {
            if (proxy == null) {
                return;
            }
            try {
                var reply = yield proxy.call(
                    "ListSnapshotFiles",
                    new GLib.Variant("(xsu)", gen, query, limit),
                    GLib.DBusCallFlags.NONE,
                    -1,
                    null
                );
                snapshot_files_updated(reply.get_child_value(0));
            } catch (Error e) {
                restore_failed(e.message);
            }
        }

        private async void restore_file_async (int64 gen, string rel_path, string dest_dir) {
            if (proxy == null) {
                restore_failed("Daemon unavailable");
                return;
            }
            try {
                var reply = yield proxy.call(
                    "RestoreFile",
                    new GLib.Variant("(xss)", gen, rel_path, dest_dir),
                    GLib.DBusCallFlags.NONE,
                    -1,
                    null
                );
                restore_finished(reply.get_child_value(0).get_string());
            } catch (Error e) {
                restore_failed(e.message);
            }
        }

        private async void set_repo_path_async (string repo_path) {
            if (proxy == null) {
                repo_path_failed("Daemon unavailable");
                return;
            }
            try {
                yield proxy.call(
                    "SetRepoPath",
                    new GLib.Variant("(s)", repo_path),
                    GLib.DBusCallFlags.NONE,
                    -1,
                    null
                );
                repo_path_changed(repo_path);
                refresh();
            } catch (Error e) {
                repo_path_failed(e.message);
            }
        }

        private async void verify_async () {
            if (proxy == null) {
                verify_failed("Walky daemon is not running");
                return;
            }
            try {
                var reply = yield proxy.call("Verify",
                    new GLib.Variant("(b)", false),
                    GLib.DBusCallFlags.NONE, 120000, null);
                verify_finished(reply.get_child_value(0).get_string());
            } catch (Error e) {
                verify_failed(e.message);
            }
        }

        private async void preview_recovery_async () {
            if (proxy == null) {
                recovery_failed("Walky daemon is not running");
                return;
            }
            try {
                var reply = yield proxy.call("PreviewRecovery", null,
                    GLib.DBusCallFlags.NONE, 600000, null);
                var body = reply.get_child_value(0);
                recovery_preview(
                    (int) body.get_child_value(0).get_int32(),
                    (int) body.get_child_value(1).get_int32(),
                    (int) body.get_child_value(2).get_int32(),
                    (int) body.get_child_value(3).get_int32(),
                    (int) body.get_child_value(4).get_int32());
            } catch (Error e) {
                recovery_failed(e.message);
            }
        }

        private async void start_recovery_async () {
            if (proxy == null) {
                recovery_failed("Walky daemon is not running");
                return;
            }
            try {
                yield proxy.call("StartRecovery", null,
                    GLib.DBusCallFlags.NONE, 15000, null);
                recovery_started();
            } catch (Error e) {
                recovery_failed(e.message);
            }
        }

        private async void fetch_events () {
            if (proxy == null) {
                return;
            }
            try {
                var reply = yield proxy.call("GetEvents", new GLib.Variant("(u)", 50u),
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
