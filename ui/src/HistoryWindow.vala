namespace Walky {
    private class SnapshotItem : GLib.Object {
        public int64 gen;
        public int64 created;
        public int64 files;
        public int64 bytes;
    }

    /**
     * Browse committed generations on the attached drive and restore individual
     * files either back into the sync folder or to a chosen directory.
     */
    public class HistoryWindow : Adw.Window {
        private Api api;
        private Adw.ToastOverlay toast_overlay;
        private Gtk.DropDown gen_dropdown;
        private Gtk.SearchEntry search_entry;
        private Gtk.ListBox files_list;
        private Gtk.Label empty_label;
        private SnapshotItem[] snapshots = {};
        private int64 selected_gen = 0;

        public HistoryWindow (Gtk.Application app, Api api) {
            Object (
                application: app,
                title: "Snapshot History",
                default_width: 680,
                default_height: 620
            );
            this.api = api;
            build_ui();

            api.snapshots_updated.connect(on_snapshots);
            api.snapshot_files_updated.connect(on_files);
            api.restore_finished.connect(on_restore_finished);
            api.restore_failed.connect(on_restore_failed);

            api.fetch_snapshots();
        }

        private void build_ui () {
            var header = new Adw.HeaderBar();
            header.title_widget = new Adw.WindowTitle("Snapshot History", "Browse and restore files from the drive");

            var controls = new Gtk.Box(Gtk.Orientation.VERTICAL, 10);
            controls.margin_top = 12;
            controls.margin_bottom = 12;
            controls.margin_start = 16;
            controls.margin_end = 16;

            gen_dropdown = new Gtk.DropDown(null, null);
            gen_dropdown.hexpand = true;
            gen_dropdown.notify["selected"].connect(() => {
                uint idx = gen_dropdown.selected;
                if (idx < snapshots.length) {
                    selected_gen = snapshots[idx].gen;
                    load_files();
                }
            });
            controls.append(gen_dropdown);

            search_entry = new Gtk.SearchEntry();
            search_entry.placeholder_text = "Filter files by name or path…";
            search_entry.search_changed.connect(() => load_files());
            controls.append(search_entry);

            empty_label = new Gtk.Label("Loading snapshots…");
            empty_label.add_css_class("dim-label");
            empty_label.margin_top = 24;
            empty_label.margin_bottom = 24;

            files_list = new Gtk.ListBox();
            files_list.add_css_class("boxed-list");
            files_list.selection_mode = Gtk.SelectionMode.NONE;
            files_list.set_placeholder(empty_label);

            var scroll = new Gtk.ScrolledWindow();
            scroll.vexpand = true;
            scroll.set_child(files_list);

            var body = new Gtk.Box(Gtk.Orientation.VERTICAL, 0);
            body.append(controls);
            var list_wrap = new Gtk.Box(Gtk.Orientation.VERTICAL, 0);
            list_wrap.margin_start = 16;
            list_wrap.margin_end = 16;
            list_wrap.margin_bottom = 16;
            list_wrap.vexpand = true;
            list_wrap.append(scroll);
            body.append(list_wrap);

            toast_overlay = new Adw.ToastOverlay();
            toast_overlay.set_child(body);

            var view = new Adw.ToolbarView();
            view.add_top_bar(header);
            view.set_content(toast_overlay);
            set_content(view);
        }

        private void on_snapshots (GLib.Variant list) {
            snapshots = {};
            var labels = new Gtk.StringList(null);
            uint n = (uint) list.n_children();
            for (uint i = 0; i < n; i++) {
                var item = list.get_child_value(i);
                var s = new SnapshotItem();
                s.gen = item.get_child_value(0).get_int64();
                s.created = item.get_child_value(1).get_int64();
                s.files = item.get_child_value(2).get_int64();
                s.bytes = item.get_child_value(3).get_int64();
                snapshots += s;
                var when = s.created > 0
                    ? new GLib.DateTime.from_unix_local(s.created).format("%Y-%m-%d %H:%M")
                    : "unknown";
                labels.append("Generation %s · %s · %s files (%s)".printf(
                    s.gen.to_string(), when, s.files.to_string(), format_bytes(s.bytes)));
            }
            gen_dropdown.model = labels;
            if (snapshots.length > 0) {
                selected_gen = snapshots[0].gen;
                gen_dropdown.selected = 0;
                load_files();
            } else {
                clear_list();
                empty_label.label = "No snapshots available (connect a Walky drive)";
            }
        }

        private void load_files () {
            if (selected_gen <= 0) {
                return;
            }
            empty_label.label = "Loading files…";
            api.fetch_snapshot_files(selected_gen, search_entry.text, 200u);
        }

        private void clear_list () {
            Gtk.Widget? child = files_list.get_first_child();
            while (child != null) {
                files_list.remove(child);
                child = files_list.get_first_child();
            }
        }

        private void on_files (GLib.Variant list) {
            clear_list();
            uint n = (uint) list.n_children();
            if (n == 0) {
                empty_label.label = search_entry.text.strip() != ""
                    ? "No matching files in this generation"
                    : "This generation is empty";
                return;
            }
            for (uint i = 0; i < n; i++) {
                var item = list.get_child_value(i);
                var rel_path = item.get_child_value(0).get_string();
                var ftype = item.get_child_value(1).get_string();
                var size = item.get_child_value(2).get_int64();
                var mtime = item.get_child_value(3).get_int64();

                var row = new Adw.ActionRow();
                row.title = GLib.Markup.escape_text(rel_path);
                var when = mtime > 0
                    ? new GLib.DateTime.from_unix_local(mtime).format("%Y-%m-%d %H:%M")
                    : "";
                row.subtitle = ftype == "l"
                    ? "symlink"
                    : (when != "" ? "%s · %s".printf(format_bytes(size), when) : format_bytes(size));

                var btn = new Gtk.Button.with_label("Restore…");
                btn.valign = Gtk.Align.CENTER;
                btn.clicked.connect(() => confirm_restore(rel_path));
                row.add_suffix(btn);
                files_list.append(row);
            }
        }

        private void confirm_restore (string rel_path) {
            var alert = new Adw.AlertDialog(
                "Restore file from generation %s?".printf(selected_gen.to_string()),
                "%s\n\nYou can overwrite the current file in your sync folder or save a copy elsewhere.".printf(rel_path)
            );
            alert.add_response("cancel", "Cancel");
            alert.add_response("other", "Save to folder…");
            alert.add_response("sync", "Restore to sync folder");
            alert.set_response_appearance("sync", Adw.ResponseAppearance.SUGGESTED);
            alert.default_response = "cancel";
            alert.close_response = "cancel";
            alert.response.connect((resp) => {
                if (resp == "sync") {
                    api.restore_file(selected_gen, rel_path, "");
                } else if (resp == "other") {
                    pick_restore_folder.begin(rel_path);
                }
            });
            alert.present(this);
        }

        private async void pick_restore_folder (string rel_path) {
            var dialog = new Gtk.FileDialog();
            dialog.title = "Choose destination folder";
            try {
                var folder = yield dialog.select_folder(this, null);
                var dest = folder.get_path();
                if (dest != null && dest != "") {
                    api.restore_file(selected_gen, rel_path, dest);
                }
            } catch (Error e) {
                // Cancelled by user.
            }
        }

        private void on_restore_finished (string restored_path) {
            toast_overlay.add_toast(new Adw.Toast("Restored: " + restored_path));
        }

        private void on_restore_failed (string message) {
            toast_overlay.add_toast(new Adw.Toast("Restore failed: " + message));
        }

        private static string format_bytes (int64 bytes) {
            if (bytes <= 0) {
                return "0 B";
            }
            string[] units = { "B", "KiB", "MiB", "GiB", "TiB" };
            double v = (double) bytes;
            int u = 0;
            while (v >= 1024.0 && u < units.length - 1) {
                v /= 1024.0;
                u++;
            }
            return u == 0 ? "%lld B".printf(bytes) : "%.1f %s".printf(v, units[u]);
        }
    }
}
