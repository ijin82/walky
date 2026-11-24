namespace Walky {
    /**
     * Main window: device state, progress and recent activity. The daemon owns
     * all synchronization; this window only observes and sends commands.
     */
    public class Window : Adw.ApplicationWindow {
        private Api api;
        private Adw.WindowTitle title_widget;
        private Adw.Banner banner;
        private Adw.ActionRow device_row;
        private Adw.ActionRow generation_row;
        private Adw.ActionRow last_sync_row;
        private Adw.ActionRow free_row;
        private Gtk.ProgressBar progress;
        private Gtk.Label progress_label;
        private Gtk.ListBox events_list;
        private Gtk.Button pause_button;
        private SettingsWindow? settings_window;
        private string last_state = "";

        public Window (Gtk.Application app, Api api) {
            Object (
                application: app,
                title: "Walky",
                default_width: 560,
                default_height: 740
            );
            this.api = api;
            build_ui();
            close_request.connect(() => {
                hide();
                return true;
            });

            api.status_updated.connect(on_status);
            api.events_updated.connect(on_events);
            api.unavailable.connect(on_unavailable);

            GLib.Timeout.add_seconds(1, () => {
                api.refresh();
                return true;
            });
            api.refresh();
        }

        private void build_ui () {
            title_widget = new Adw.WindowTitle("Walky", "Connecting…");

            var header = new Adw.HeaderBar();
            header.set_title_widget(title_widget);
            header.pack_end(build_menu_button());

            banner = new Adw.Banner("Daemon unavailable");
            banner.revealed = false;

            var content = new Gtk.Box(Gtk.Orientation.VERTICAL, 18);
            content.margin_top = 18;
            content.margin_bottom = 18;
            content.margin_start = 18;
            content.margin_end = 18;
            content.append(banner);
            content.append(build_status_group());
            content.append(build_progress());
            content.append(build_events_group());
            content.append(build_actions());

            var scroll = new Gtk.ScrolledWindow();
            scroll.set_child(content);
            scroll.vexpand = true;

            var view = new Adw.ToolbarView();
            view.add_top_bar(header);
            view.set_content(scroll);
            set_content(view);
        }

        private Gtk.Widget build_menu_button () {
            var menu = new GLib.Menu();
            menu.append("Sync now", "win.sync");
            menu.append("Pause / resume", "win.pause");
            menu.append("Verify integrity", "win.verify");
            menu.append("Eject drive", "win.eject");
            menu.append("Settings…", "win.settings");
            menu.append("Quit", "app.quit");

            var button = new Gtk.MenuButton();
            button.icon_name = "open-menu-symbolic";
            button.menu_model = menu;
            button.tooltip_text = "Main menu";

            var actions = new GLib.SimpleActionGroup();

            var sync_action = new GLib.SimpleAction("sync", null);
            sync_action.activate.connect(() => api.sync_now());
            actions.add_action(sync_action);

            var pause_action = new GLib.SimpleAction("pause", null);
            pause_action.activate.connect(toggle_pause);
            actions.add_action(pause_action);

            var verify_action = new GLib.SimpleAction("verify", null);
            verify_action.activate.connect(() => api.verify());
            actions.add_action(verify_action);

            var eject_action = new GLib.SimpleAction("eject", null);
            eject_action.activate.connect(() => api.eject());
            actions.add_action(eject_action);

            var settings_action = new GLib.SimpleAction("settings", null);
            settings_action.activate.connect(open_settings);
            actions.add_action(settings_action);

            insert_action_group("win", actions);
            return button;
        }

        private Adw.PreferencesGroup build_status_group () {
            var group = new Adw.PreferencesGroup();
            group.title = "Status";

            device_row = new Adw.ActionRow();
            device_row.title = "Device";
            device_row.subtitle = "No drive";
            group.add(device_row);

            generation_row = new Adw.ActionRow();
            generation_row.title = "Generation";
            generation_row.subtitle = "—";
            group.add(generation_row);

            last_sync_row = new Adw.ActionRow();
            last_sync_row.title = "Last sync";
            last_sync_row.subtitle = "never";
            group.add(last_sync_row);

            free_row = new Adw.ActionRow();
            free_row.title = "Free space";
            free_row.subtitle = "—";
            group.add(free_row);
            return group;
        }

        private Gtk.Widget build_progress () {
            var box = new Gtk.Box(Gtk.Orientation.VERTICAL, 6);
            progress = new Gtk.ProgressBar();
            progress.visible = false;
            progress.hexpand = true;
            progress_label = new Gtk.Label("");
            progress_label.halign = Gtk.Align.START;
            progress_label.add_css_class("dim-label");
            box.append(progress);
            box.append(progress_label);
            return box;
        }

        private Adw.PreferencesGroup build_events_group () {
            var group = new Adw.PreferencesGroup();
            group.title = "Recent files";

            events_list = new Gtk.ListBox();
            events_list.add_css_class("boxed-list");
            events_list.selection_mode = Gtk.SelectionMode.NONE;
            group.add(events_list);
            return group;
        }

        private Gtk.Widget build_actions () {
            var box = new Gtk.Box(Gtk.Orientation.HORIZONTAL, 12);
            box.halign = Gtk.Align.CENTER;
            box.margin_top = 6;

            var sync_button = new Gtk.Button.with_label("Sync now");
            sync_button.add_css_class("suggested-action");
            sync_button.clicked.connect(() => api.sync_now());
            box.append(sync_button);

            pause_button = new Gtk.Button.with_label("Pause");
            pause_button.clicked.connect(toggle_pause);
            box.append(pause_button);

            var eject_button = new Gtk.Button.with_label("Eject");
            eject_button.clicked.connect(() => api.eject());
            box.append(eject_button);
            return box;
        }

        private void open_settings () {
            if (settings_window == null) {
                settings_window = new SettingsWindow(application as Gtk.Application, api);
            }
            settings_window.present();
        }

        private void toggle_pause () {
            bool paused = pause_button.label == "Resume";
            api.pause(!paused);
        }

        private void on_status (GLib.Variant status) {
            var state = string_value(status, "state");
            title_widget.subtitle = state_text(state);

            var device = string_value(status, "device");
            var root = string_value(status, "store_root");
            if (root != "") {
                device_row.subtitle = device != "" ? "%s · %s".printf(device, root) : root;
            } else {
                device_row.subtitle = "No drive";
            }

            int64 generation = int_value(status, "generation");
            generation_row.subtitle = generation > 0 ? generation.to_string() : "—";

            int64 last_sync = int_value(status, "last_sync");
            last_sync_row.subtitle = last_sync > 0
                ? new GLib.DateTime.from_unix_local(last_sync).format("%Y-%m-%d %H:%M:%S")
                : "never";

            free_row.subtitle = human_bytes(int_value(status, "free_bytes"));

            uint32 done = uint_value(status, "progress_done");
            uint32 total = uint_value(status, "progress_total");
            bool syncing = state == "syncing";
            progress.visible = syncing || total > 0;
            progress.fraction = total > 0 ? (double) done / (double) total : 0.0;
            progress_label.label = total > 0
                ? "%u / %u files".printf(done, total)
                : (syncing ? "Scanning…" : "");

            bool paused = bool_value(status, "paused");
            pause_button.label = paused ? "Resume" : "Pause";

            var error = string_value(status, "last_error");
            banner.revealed = error != "";
            if (error != "") {
                banner.title = error;
            }

            if (state == "idle" && last_state == "syncing") {
                send_notification("Sync finished", "Generation %s".printf(generation.to_string()));
            } else if (state == "error" && last_state != "error") {
                send_notification("Sync failed", error);
            }
            last_state = state;
        }

        private void on_events (GLib.Variant events) {
            Gtk.Widget? child = events_list.get_first_child();
            while (child != null) {
                events_list.remove(child);
                child = events_list.get_first_child();
            }

            uint count = (uint) events.n_children();
            if (count == 0) {
                var empty = new Adw.ActionRow();
                empty.title = "No activity yet";
                events_list.append(empty);
                return;
            }
            for (uint i = 0; i < (uint) count; i++) {
                var item = events.get_child_value(i);
                var row = new Adw.ActionRow();
                row.title = item.get_child_value(0).get_string();
                var op = item.get_child_value(1).get_string();
                var result = item.get_child_value(2).get_string();
                int64 size = item.get_child_value(3).get_int64();
                int64 ts = item.get_child_value(4).get_int64();
                var time = ts > 0
                    ? new GLib.DateTime.from_unix_local(ts).format("%H:%M:%S")
                    : "";
                if (size > 0) {
                    row.subtitle = "%s · %s · %s".printf(op_text(op), human_bytes(size), time);
                } else {
                    row.subtitle = "%s · %s".printf(op_text(op), time);
                }
                if (result != "ok" && result != "") {
                    row.subtitle += " · " + result;
                }
                events_list.append(row);
            }
        }

        private void on_unavailable (string message) {
            if (last_state == "offline") {
                return;
            }
            last_state = "offline";
            title_widget.subtitle = "Daemon is not running";
            banner.title = message;
            banner.revealed = true;
            device_row.subtitle = "No drive";
            progress.visible = false;
        }

        private void send_notification (string heading, string body) {
            var notification = new GLib.Notification(heading);
            notification.set_body(body);
            var app = application as Gtk.Application;
            if (app != null) {
                app.send_notification(null, notification);
            }
        }

        private static string state_text (string state) {
            switch (state) {
                case "idle": return "Up to date";
                case "syncing": return "Syncing…";
                case "paused": return "Paused";
                case "error": return "Error";
                case "no_device": return "No drive";
                default: return state;
            }
        }

        private static string op_text (string op) {
            switch (op) {
                case "push": return "Uploaded";
                case "pull": return "Downloaded";
                case "delete": return "Deleted";
                case "conflict": return "Conflict";
                case "attach": return "Attached";
                case "detach": return "Detached";
                case "sync": return "Sync";
                default: return op;
            }
        }

        private static string string_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("s"));
            return value != null ? value.get_string() : "";
        }

        private static int64 int_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("x"));
            return value != null ? value.get_int64() : 0;
        }

        private static uint32 uint_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("u"));
            return value != null ? value.get_uint32() : 0;
        }

        private static bool bool_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("b"));
            return value != null ? value.get_boolean() : false;
        }

        private static string human_bytes (int64 bytes) {
            if (bytes <= 0) {
                return "—";
            }
            string[] units = { "B", "KiB", "MiB", "GiB", "TiB" };
            double value = bytes;
            int unit = 0;
            while (value >= 1024 && unit < units.length - 1) {
                value /= 1024;
                unit++;
            }
            return "%.1f %s".printf(value, units[unit]);
        }
    }
}
