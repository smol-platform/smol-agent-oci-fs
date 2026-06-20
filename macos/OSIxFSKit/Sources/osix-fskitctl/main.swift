import Foundation
import FSKit

let defaultBundleID = "io.github.smol-platform.smol-agent-oci-fs.fskit.extension"
let defaultFileSystemType = "OSIxFS"

struct CLIError: Error, CustomStringConvertible {
    let message: String
    let code: Int32

    var description: String { message }
}

@main
struct OSIxFSKitControl {
    static func main() async {
        do {
            try await run()
            Foundation.exit(0)
        } catch let error as CLIError {
            fputs(error.description + "\n", stderr)
            Foundation.exit(error.code)
        } catch {
            fputs(String(describing: error) + "\n", stderr)
            Foundation.exit(70)
        }
    }

    static func run() async throws {
        var args = Array(CommandLine.arguments.dropFirst())
        guard let command = args.first else {
            throw usage("missing command")
        }
        args.removeFirst()

        switch command {
        case "doctor":
            let opts = parseOptions(args)
            try await requireReady(bundleID: opts["bundle-id"] ?? environment("OSIX_FSKIT_BUNDLE_ID", defaultBundleID))
        case "mount":
            let opts = parseOptions(args)
            try await mount(opts)
        case "unmount":
            let opts = parseOptions(args)
            try unmount(opts)
        default:
            throw usage("unknown command: \(command)")
        }
    }

    static func mount(_ opts: [String: String]) async throws {
        let bundleID = opts["bundle-id"] ?? environment("OSIX_FSKIT_BUNDLE_ID", defaultBundleID)
        try await requireReady(bundleID: bundleID)

        let target = try required(opts, "target")
        let sourceRef = try required(opts, "source-ref")
        let sourceDigest = try required(opts, "source-digest")
        let workspaceRoot = try required(opts, "workspace-root")
        let lower = try required(opts, "lower")
        let upper = try required(opts, "upper")
        let work = try required(opts, "work")
        let mode = opts["mode"] ?? "overlay"
        let fsType = opts["fstype"] ?? environment("OSIX_FSKIT_TYPE", defaultFileSystemType)

        try FileManager.default.createDirectory(atPath: target, withIntermediateDirectories: true)
        let mountOptions = [
            "osix.bundle=" + encode(bundleID),
            "osix.workspace=" + encode(workspaceRoot),
            "osix.source_ref=" + encode(sourceRef),
            "osix.source_digest=" + encode(sourceDigest),
            "osix.lower=" + encode(lower),
            "osix.upper=" + encode(upper),
            "osix.work=" + encode(work),
            "osix.mode=" + encode(mode)
        ].joined(separator: ",")

        try runProcess("/sbin/mount", ["-F", "-t", fsType, "-o", mountOptions, "osixfs", target])
    }

    static func unmount(_ opts: [String: String]) throws {
        let target = try required(opts, "target")
        if opts.keys.contains("force") {
            try runProcess("/usr/sbin/diskutil", ["unmount", "force", target])
            return
        }
        try runProcess("/sbin/umount", [target])
    }

    static func requireReady(bundleID: String) async throws {
        guard FileManager.default.fileExists(atPath: "/System/Library/Frameworks/FSKit.framework") else {
            throw CLIError(message: "FSKit.framework is unavailable; macOS 15.4 or newer is required", code: 69)
        }
        guard #available(macOS 15.4, *) else {
            throw CLIError(message: "FSKit.framework is unavailable; macOS 15.4 or newer is required", code: 69)
        }

        let modules: [FSModuleIdentity]
        do {
            modules = try await FSClient.shared.installedExtensions
        } catch {
            throw CLIError(message: "failed to query installed FSKit extensions: \(error)", code: 69)
        }

        guard let module = modules.first(where: { $0.bundleIdentifier == bundleID }) else {
            if let plugInKitState = try? plugInKitRegistrationState(bundleID: bundleID) {
                throw CLIError(
                    message: "FSKit extension \(bundleID) is \(plugInKitState) but FSClient does not report it as enabled; enable it in System Settings > General > Login Items & Extensions > File System Extensions",
                    code: 69
                )
            }
            throw CLIError(message: "FSKit extension \(bundleID) is not installed or not discoverable", code: 69)
        }
        guard module.isEnabled else {
            let plugInKitState = (try? plugInKitRegistrationState(bundleID: bundleID)) ?? "installed"
            throw CLIError(
                message: "FSKit extension \(bundleID) is \(plugInKitState) but not enabled for FSKit; enable it in System Settings > General > Login Items & Extensions > File System Extensions",
                code: 69
            )
        }
    }

    static func plugInKitRegistrationState(bundleID: String) throws -> String {
        let output = try runProcessCapturing("/usr/bin/pluginkit", ["-m", "-A", "-D", "-vv", "-i", bundleID])
        guard output.contains(bundleID) else {
            throw CLIError(message: "FSKit extension \(bundleID) is not installed or not discoverable", code: 69)
        }
        return plugInKitOutputShowsEnabled(output, bundleID: bundleID) ? "registered/elected in PlugInKit" : "registered in PlugInKit"
    }

    static func plugInKitOutputShowsEnabled(_ output: String, bundleID: String) -> Bool {
        output.split(separator: "\n").contains { line in
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            return trimmed.hasPrefix("+") && trimmed.contains(bundleID)
        }
    }
}

func parseOptions(_ args: [String]) -> [String: String] {
    var opts: [String: String] = [:]
    var index = 0
    while index < args.count {
        let arg = args[index]
        if arg.hasPrefix("--") {
            let key = String(arg.dropFirst(2))
            if index + 1 < args.count && !args[index + 1].hasPrefix("--") {
                opts[key] = args[index + 1]
                index += 2
            } else {
                opts[key] = "true"
                index += 1
            }
        } else {
            index += 1
        }
    }
    return opts
}

func required(_ opts: [String: String], _ key: String) throws -> String {
    guard let value = opts[key], !value.isEmpty else {
        throw usage("missing --\(key)")
    }
    return value
}

func usage(_ message: String) -> CLIError {
    CLIError(message: "\(message)\nusage: osix-fskitctl doctor --bundle-id BUNDLE_ID\n       osix-fskitctl mount --target PATH --lower PATH --upper PATH --work PATH --source-ref REF --source-digest DIGEST --workspace-root PATH\n       osix-fskitctl unmount --target PATH [--force]", code: 64)
}

func environment(_ key: String, _ fallback: String) -> String {
    let value = ProcessInfo.processInfo.environment[key] ?? ""
    return value.isEmpty ? fallback : value
}

func encode(_ value: String) -> String {
    Data(value.utf8)
        .base64EncodedString()
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_")
        .replacingOccurrences(of: "=", with: "")
}

func runProcess(_ executable: String, _ arguments: [String]) throws {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: executable)
    process.arguments = arguments

    let pipe = Pipe()
    process.standardError = pipe
    process.standardOutput = pipe

    try process.run()
    process.waitUntilExit()

    if process.terminationStatus != 0 {
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        throw CLIError(message: output.isEmpty ? "\(executable) exited \(process.terminationStatus)" : output, code: process.terminationStatus)
    }
}

func runProcessCapturing(_ executable: String, _ arguments: [String]) throws -> String {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: executable)
    process.arguments = arguments

    let pipe = Pipe()
    process.standardError = pipe
    process.standardOutput = pipe

    try process.run()
    process.waitUntilExit()

    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    let output = String(data: data, encoding: .utf8) ?? ""
    if process.terminationStatus != 0 {
        let message = output.trimmingCharacters(in: .whitespacesAndNewlines)
        throw CLIError(message: message.isEmpty ? "\(executable) exited \(process.terminationStatus)" : message, code: process.terminationStatus)
    }
    return output
}
