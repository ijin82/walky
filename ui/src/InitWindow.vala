namespace Walky {
    public struct DriveInfo {
        public string mount_point;
        public string label;
        public string store_root;
        public string repo_id;
        public bool initialized;
        public int64 free_bytes;
        public bool encrypted;
    }

    /**
     * First-run wizard: pick a mounted removable drive, pick the local folder and
     * create or adopt the store. All work happens in the daemon through the same
     * setup package the CLI uses.
     */
    public class InitWindow : Adw.ApplicationWindow {
        private Api api;
        private Adw.ComboRow drive_row;
        private Gtk.Entry repo_entry;
        private Gtk.Button action_button;
        private Gtk.Label hint;
        private Adw.ToastOverlay toast_overlay;
        private DriveInfo[] drives = {};

        public InitWindow (Gtk.Application app, Api api) {
            Object (
                application: app,
                title: "Set up a drive",
                default_width: 600,
                default_height: 560
            );
            this.api = api;
            build_ui();
            api.drives_updated.connect(on_drives);
            api.store_ready.connect(on_ready);
            api.store_failed.connect(on_failed);
            api.fetch_drives();
        }

        private void build_ui () {
            var header = new Adw.HeaderBar();
            var title = new Adw.WindowTitle("Set up a drive", "Choose a removable ext4 drive");
            header.set_title_widget(title);
            var refresh = new Gtk.Button.from_icon_name("view-refresh-symbolic");
            refresh.tooltip_text = "Refresh drives";
            refresh.clicked.connect(() => api.fetch_drives());
            header.pack_end(refresh);

            var box = new Gtk.Box(Gtk.Orientation.VERTICAL, 18);
            box.margin_top = 18;
            box.margin_bottom = 18;
            box.margin_start = 18;
            box.margin_end = 18;

            var drive_group = new Adw.PreferencesGroup();
            drive_group.title = "Drive";
            drive_group.description = "Only removable ext4 filesystems are listed.";
            drive_row = new Adw.ComboRow();
            drive_row.title = "Mounted drive";
            drive_row.notify["selected"].connect(() => update_action());
            drive_group.add(drive_row);

            hint = new Gtk.Label("");
            hint.halign = Gtk.Align.START;
            hint.wrap = true;
            hint.add_css_class("dim-label");
            drive_group.add(hint);
            box.append(drive_group);

            var repo_group = new Adw.PreferencesGroup();
            repo_group.title = "Local folder";
            repo_group.description = "This folder will be synchronized with the drive.";
            var repo_box = new Gtk.Box(Gtk.Orientation.HORIZONTAL, 6);
            repo_entry = new Gtk.Entry();
            repo_entry.hexpand = true;
            repo_entry.text = GLib.Environment.get_home_dir() + "/Sync";
            repo_box.append(repo_entry);
            var browse = new Gtk.Button.from_icon_name("folder-open-symbolic");
            browse.tooltip_text = "Choose folder";
            browse.clicked.connect(() => browse_folder.begin());
            repo_box.append(browse);
            repo_group.add(repo_box);
            box.append(repo_group);

            var info = new Gtk.Label(
                "Initialization only creates /WALKY on the drive. Existing files are not touched.");
            info.wrap = true;
            info.halign = Gtk.Align.START;
            info.add_css_class("dim-label");
            box.append(info);

            action_button = new Gtk.Button.with_label("Initialize drive");
            action_button.add_css_class("suggested-action");
            action_button.halign = Gtk.Align.END;
            action_button.sensitive = false;
            action_button.clicked.connect(activate);
            box.append(action_button);

            var scroll = new Gtk.ScrolledWindow();
            scroll.set_child(box);
            scroll.vexpand = true;

            toast_overlay = new Adw.ToastOverlay();
            toast_overlay.set_child(scroll);

            var view = new Adw.ToolbarView();
            view.add_top_bar(header);
            view.set_content(toast_overlay);
            set_content(view);
        }

        private async void browse_folder () {
            var dialog = new Gtk.FileDialog();
            dialog.title = "Choose the folder to sync";
            try {
                var file = yield dialog.select_folder(this, null);
                if (file != null) {
                    var path = file.get_path();
                    if (path != null) {
                        repo_entry.text = path;
                    }
                }
            } catch (Error e) {
                // Cancelled by the user.
            }
        }

        private void on_drives (GLib.Variant value) {
            drives = {};
            var model = new Gtk.StringList(null);
            uint count = (uint) value.n_children();
            for (uint i = 0; i < count; i++) {
                var item = value.get_child_value(i);
                var drive = DriveInfo();
                drive.mount_point = item.get_child_value(0).get_string();
                drive.label = item.get_child_value(1).get_string();
                drive.store_root = item.get_child_value(2).get_string();
                drive.repo_id = item.get_child_value(3).get_string();
                drive.initialized = item.get_child_value(4).get_boolean();
                drive.free_bytes = item.get_child_value(5).get_int64();
                drive.encrypted = item.get_child_value(6).get_boolean();
                drives += drive;

                var text = "%s — %s, %s free".printf(
                    drive.label, drive.mount_point, human_bytes(drive.free_bytes));
                if (drive.encrypted) {
                    text += " · LUKS";
                }
                if (drive.initialized) {
                    text += " · store ready";
                }
                model.append(text);
            }
            drive_row.model = model;
            if (count == 0) {
                hint.label = "No removable ext4 drives found. Plug the drive in and press refresh.";
                action_button.sensitive = false;
            } else {
                hint.label = "Select the drive, then choose the local folder.";
                drive_row.selected = 0;
                action_button.sensitive = true;
                update_action();
            }
        }

        private void update_action () {
            if (drives.length == 0) {
                action_button.sensitive = false;
                return;
            }
            var index = drive_row.selected;
            if (index >= drives.length) {
                index = 0;
            }
            action_button.label = drives[index].initialized ? "Use this drive" : "Initialize drive";
        }

        private void activate () {
            if (drives.length == 0) {
                return;
            }
            var index = drive_row.selected;
            if (index >= drives.length) {
                index = 0;
            }
            var drive = drives[index];
            var repo = repo_entry.text.strip();
            if (repo == "") {
                toast_overlay.add_toast(new Adw.Toast("Choose a local folder first"));
                return;
            }
            if (drive.initialized) {
                api.use_store(drive.mount_point, repo);
            } else {
                api.init_store(drive.mount_point, repo);
            }
        }

        private void on_ready (string repo_id) {
            toast_overlay.add_toast(new Adw.Toast("Store is ready: " + repo_id));
            GLib.Timeout.add_seconds(2, () => {
                close();
                return false;
            });
        }

        private void on_failed (string message) {
            toast_overlay.add_toast(new Adw.Toast("Cannot use this drive: " + message));
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
