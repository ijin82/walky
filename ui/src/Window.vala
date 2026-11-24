namespace Walky {
    /**
     * Main window skeleton: device card, progress and recent files land here
     * in M6. For now it only proves the GTK4/libadwaita stack builds.
     */
    public class Window : Adw.ApplicationWindow {
        public Window (Gtk.Application app) {
            Object (
                application: app,
                title: "Walky",
                default_width: 560,
                default_height: 680
            );

            var title = new Adw.WindowTitle ("Walky", "Not connected");

            var header = new Adw.HeaderBar ();
            header.set_title_widget (title);

            var status = new Adw.StatusPage ();
            status.icon_name = "drive-removable-media-symbolic";
            status.title = "No drive";
            status.description = "Plug in the Walky drive to start syncing.";

            var view = new Adw.ToolbarView ();
            view.add_top_bar (header);
            view.set_content (status);
            content = view;
        }
    }
}
