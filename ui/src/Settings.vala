namespace Walky {
    /**
     * Editable subset of the configuration. Values are sent to the daemon,
     * which validates, persists and applies them; the UI never writes TOML.
     */
    public class SettingsWindow : Adw.PreferencesWindow {
        private Api api;
        private Gtk.TextView excludes_view;
        private Adw.SpinRow daily_row;
        private Adw.SpinRow weekly_row;
        private Adw.SpinRow monthly_row;
        private Adw.SpinRow trash_row;
        private Adw.ComboRow conflict_row;

        public SettingsWindow (Gtk.Application app, Api api) {
            Object (
                application: app,
                title: "Walky Settings",
                default_width: 620,
                default_height: 720
            );
            this.api = api;
            build_ui();
            api.settings_updated.connect(on_settings);
            api.settings_saved.connect(on_saved);
            api.settings_failed.connect(on_failed);
            api.fetch_settings();
        }

        private void build_ui () {
            var page = new Adw.PreferencesPage();
            add(page);

            var excludes_group = new Adw.PreferencesGroup();
            excludes_group.title = "Excluded paths";
            excludes_group.description = "One glob per line, relative to the sync folder.";
            excludes_view = new Gtk.TextView();
            excludes_view.monospace = true;
            excludes_view.wrap_mode = Gtk.WrapMode.WORD_CHAR;
            var scroll = new Gtk.ScrolledWindow();
            scroll.set_child(excludes_view);
            scroll.set_size_request(-1, 140);
            scroll.add_css_class("card");
            excludes_group.add(scroll);
            page.add(excludes_group);

            var retention_group = new Adw.PreferencesGroup();
            retention_group.title = "Retention";
            retention_group.description = "Generations kept on the drive.";
            daily_row = new Adw.SpinRow(new Gtk.Adjustment(0, 0, 3650, 1, 10, 0), 1, 0);
            daily_row.title = "Daily";
            retention_group.add(daily_row);
            weekly_row = new Adw.SpinRow(new Gtk.Adjustment(0, 0, 520, 1, 4, 0), 1, 0);
            weekly_row.title = "Weekly";
            retention_group.add(weekly_row);
            monthly_row = new Adw.SpinRow(new Gtk.Adjustment(0, 0, 120, 1, 3, 0), 1, 0);
            monthly_row.title = "Monthly";
            retention_group.add(monthly_row);
            trash_row = new Adw.SpinRow(new Gtk.Adjustment(0, 0, 3650, 1, 5, 0), 1, 0);
            trash_row.title = "Trash days";
            retention_group.add(trash_row);
            page.add(retention_group);

            var sync_group = new Adw.PreferencesGroup();
            sync_group.title = "Sync";
            conflict_row = new Adw.ComboRow();
            conflict_row.title = "Conflict policy";
            var model = new Gtk.StringList(null);
            model.append("Keep both");
            model.append("Newest wins");
            model.append("Local wins");
            conflict_row.model = model;
            sync_group.add(conflict_row);
            page.add(sync_group);

            var actions = new Adw.PreferencesGroup();
            var save = new Gtk.Button.with_label("Save settings");
            save.add_css_class("suggested-action");
            save.halign = Gtk.Align.END;
            save.clicked.connect(save_settings);
            actions.add(save);
            page.add(actions);
        }

        private void on_settings (GLib.Variant dict) {
            var excludes = dict.lookup_value("excludes", new GLib.VariantType("as"));
            if (excludes != null) {
                var builder = new GLib.StringBuilder();
                foreach (unowned string pattern in excludes.get_strv()) {
                    if (builder.len > 0) {
                        builder.append("\n");
                    }
                    builder.append(pattern);
                }
                excludes_view.buffer.text = builder.str;
            }
            daily_row.value = int_value(dict, "daily");
            weekly_row.value = int_value(dict, "weekly");
            monthly_row.value = int_value(dict, "monthly");
            trash_row.value = int_value(dict, "trash_days");
            var conflict = string_value(dict, "conflict");
            conflict_row.selected = conflict_index(conflict);
        }

        private void save_settings () {
            string[] patterns = {};
            foreach (unowned string line in excludes_view.buffer.text.split("\n")) {
                var trimmed = line.strip();
                if (trimmed != "") {
                    patterns += trimmed;
                }
            }
            api.save_settings(
                patterns,
                (int) daily_row.value,
                (int) weekly_row.value,
                (int) monthly_row.value,
                (int) trash_row.value,
                conflict_value(conflict_row.selected)
            );
        }

        private void on_saved () {
            add_toast(new Adw.Toast("Settings saved"));
        }

        private void on_failed (string message) {
            add_toast(new Adw.Toast("Cannot save: " + message));
        }

        private static int int_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("i"));
            return value != null ? value.get_int32() : 0;
        }

        private static string string_value (GLib.Variant dict, string key) {
            var value = dict.lookup_value(key, new GLib.VariantType("s"));
            return value != null ? value.get_string() : "";
        }

        private static string conflict_value (uint index) {
            switch (index) {
                case 1: return "newest";
                case 2: return "local-wins";
                default: return "copy";
            }
        }

        private static uint conflict_index (string value) {
            switch (value) {
                case "newest": return 1;
                case "local-wins": return 2;
                default: return 0;
            }
        }
    }
}
