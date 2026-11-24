namespace Walky {
    /**
     * Shown when the attached drive belongs to another store. Explains the
     * additive recovery: nothing is deleted, conflicts keep both variants and
     * the history starts fresh.
     */
    public class RecoveryWindow : Adw.ApplicationWindow {
        private Api api;
        private Gtk.Label summary;
        private Gtk.Button merge_button;
        private Adw.ToastOverlay toast_overlay;

        public RecoveryWindow (Gtk.Application app, Api api) {
            Object (
                application: app,
                title: "Recover this drive",
                default_width: 560,
                default_height: 420
            );
            this.api = api;
            build_ui();
            api.recovery_preview.connect(on_preview);
            api.recovery_started.connect(on_started);
            api.recovery_failed.connect(on_failed);
            api.preview_recovery();
        }

        private void build_ui () {
            var header = new Adw.HeaderBar();
            var title = new Adw.WindowTitle("Recover this drive", "Different repository");
            header.set_title_widget(title);

            var content = new Gtk.Box(Gtk.Orientation.VERTICAL, 14);
            content.margin_top = 18;
            content.margin_bottom = 18;
            content.margin_start = 18;
            content.margin_end = 18;

            var warning = new Gtk.Label(
                "This drive contains a different Walky store. Merging starts a new history. " +
                "No files are deleted: files that exist only on this machine or only on the " +
                "drive are kept, and changed files with the same name keep both variants.");
            warning.wrap = true;
            warning.halign = Gtk.Align.START;
            content.append(warning);

            summary = new Gtk.Label("Scanning both sides…");
            summary.wrap = true;
            summary.halign = Gtk.Align.START;
            summary.add_css_class("dim-label");
            content.append(summary);

            var actions = new Gtk.Box(Gtk.Orientation.HORIZONTAL, 8);
            actions.halign = Gtk.Align.END;
            var cancel = new Gtk.Button.with_label("Cancel");
            cancel.clicked.connect(() => close());
            actions.append(cancel);
            merge_button = new Gtk.Button.with_label("Merge without deletions");
            merge_button.add_css_class("suggested-action");
            merge_button.sensitive = false;
            merge_button.clicked.connect(() => api.start_recovery());
            actions.append(merge_button);
            content.append(actions);

            var scroll = new Gtk.ScrolledWindow();
            scroll.set_child(content);
            scroll.vexpand = true;

            toast_overlay = new Adw.ToastOverlay();
            toast_overlay.set_child(scroll);

            var view = new Adw.ToolbarView();
            view.add_top_bar(header);
            view.set_content(toast_overlay);
            set_content(view);
        }

        private void on_preview (int push, int pull, int conflicts, int deletes, int total) {
            summary.label = "To upload: %d · to download: %d · conflicts: %d · deletions: %d".printf(
                push, pull, conflicts, deletes);
            merge_button.sensitive = true;
        }

        private void on_started () {
            toast_overlay.add_toast(new Adw.Toast("Recovery started"));
            GLib.Timeout.add_seconds(1, () => {
                close();
                return false;
            });
        }

        private void on_failed (string message) {
            toast_overlay.add_toast(new Adw.Toast("Recovery failed: " + message));
        }
    }
}
