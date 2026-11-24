namespace Walky {
    /**
     * Application entry point. The UI is a thin client: all sync state lives
     * in the Go daemon and is reached over D-Bus.
     */
    public class Application : Adw.Application {
        private Api api;
        private Window? window;

        public Application () {
            Object (
                application_id: "io.github.ijin.Walky",
                flags: ApplicationFlags.DEFAULT_FLAGS
            );
            api = new Api();
        }

        protected override void activate () {
            if (window == null) {
                window = new Window (this, api);
            }
            window.present ();
        }

    }
}
