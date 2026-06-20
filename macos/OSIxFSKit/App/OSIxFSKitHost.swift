import AppKit

@main
final class OSIxFSKitHost: NSObject, NSApplicationDelegate {
    static func main() {
        let app = NSApplication.shared
        let delegate = OSIxFSKitHost()
        app.delegate = delegate
        app.setActivationPolicy(.accessory)
        app.run()
    }
}
