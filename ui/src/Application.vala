namespace Walky {
    /**
     * Application entry point. The UI is a thin client: all sync state lives
     * in the Go daemon and is reached over D-Bus.
     */
    public class Application : Adw.Application {
        private Api api;

        public Application () {
            Object (
                application_id: "io.github.ijin.Walky",
                flags: ApplicationFlags.DEFAULT_FLAGS
            );
            api = new Api();
        }

        protected override void activate () {
            var win = new Window (this, api);
            win.present ();
        }
    }
}
