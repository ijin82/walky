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
            request_background();
        }

        protected override void activate () {
            if (window == null) {
                window = new Window (this, api);
            }
            window.present ();
        }

        // Ask the desktop to keep the app listed as running in the background.
        // Not every portal implementation supports it; failure is harmless.
        private void request_background () {
            try {
                var proxy = new GLib.DBusProxy.for_bus_sync(
                    GLib.BusType.SESSION, GLib.DBusProxyFlags.NONE, null,
                    "org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop",
                    "org.freedesktop.portal.Background", null);
                var builder = new GLib.VariantBuilder(new GLib.VariantType("a{sv}"));
                builder.add("{sv}", "reason",
                    new GLib.Variant("s", "Walky keeps the drive in sync in the background"));
                builder.add("{sv}", "autostart", new GLib.Variant("b", false));
                var args = new GLib.Variant("(sa{sv})", "", builder);
                proxy.call.begin("RequestBackground", args,
                    GLib.DBusCallFlags.NONE, 5000, null, (obj, res) => {
                        try {
                            var handle = proxy.call.end(res);
                            debug("background portal registered: %s", handle.print(true));
                        } catch (Error e) {
                            debug("background portal failed: %s", e.message);
                        }
                    });
            } catch (Error e) {
                debug("background portal unavailable: %s", e.message);
            }
        }
    }
}
