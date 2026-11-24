namespace Walky {
    /**
     * Application entry point. The UI is a thin client: all sync state lives
     * in the Go daemon and is reached over D-Bus (M6).
     */
    public class Application : Adw.Application {
        public Application () {
            Object (
                application_id: "io.github.ijin.Walky",
                flags: ApplicationFlags.DEFAULT_FLAGS
            );
        }

        protected override void activate () {
            var win = new Window (this);
            win.present ();
        }
    }
}
