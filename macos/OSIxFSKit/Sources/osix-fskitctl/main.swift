import Darwin
import Foundation
import FSKit

let defaultBundleID = "io.github.smol-platform.smol-agent-oci-fs.fskit.extension"
let defaultFileSystemType = "OSIxFS"
let fileSystemExtensionSettingsPath = "System Settings > Login Items & Extensions > OSIxFSKitHost Extensions > FSKit Modules"

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
            let opts = try parseOptions(args, allowed: ["bundle-id", "fstype"])
            let bundleID = try optionOrEnvironment(opts, "bundle-id", envKey: "OSIX_FSKIT_BUNDLE_ID", fallback: defaultBundleID)
            let fsType = try optionOrEnvironment(opts, "fstype", envKey: "OSIX_FSKIT_TYPE", fallback: defaultFileSystemType)
            try await requireReady(bundleID: bundleID, fileSystemType: fsType)
        case "mount":
            let opts = try parseOptions(args, allowed: [
                "bundle-id",
                "fstype",
                "lower",
                "mode",
                "source-digest",
                "source-ref",
                "target",
                "upper",
                "work",
                "workspace-root",
                "rw",
            ])
            try await mount(opts)
        case "unmount":
            let opts = try parseOptions(args, allowed: ["force", "target"])
            try unmount(opts)
        default:
            throw usage("unknown command: \(command)")
        }
    }

    static func mount(_ opts: [String: String]) async throws {
        let bundleID = try optionOrEnvironment(opts, "bundle-id", envKey: "OSIX_FSKIT_BUNDLE_ID", fallback: defaultBundleID)
        let fsType = try optionOrEnvironment(opts, "fstype", envKey: "OSIX_FSKIT_TYPE", fallback: defaultFileSystemType)

        let target = try required(opts, "target")
        let sourceRef = try required(opts, "source-ref")
        let sourceDigest = try required(opts, "source-digest")
        let workspaceRoot = try required(opts, "workspace-root")
        let lower = try required(opts, "lower")
        let upper = try required(opts, "upper")
        let work = try required(opts, "work")
        let mode = opts["mode"] ?? "overlay"
        let rw = try readWriteOption(opts)
        try validateMountMode(mode)
        try validateSourceDigest(sourceDigest)
        try validateMountPaths(target: target, workspaceRoot: workspaceRoot, lower: lower, upper: upper, work: work)
        try await requireReady(bundleID: bundleID, fileSystemType: fsType)

        try FileManager.default.createDirectory(atPath: target, withIntermediateDirectories: true)
        let mountOptions = [
            "osix.bundle=" + encode(bundleID),
            "osix.workspace=" + encode(workspaceRoot),
            "osix.source_ref=" + encode(sourceRef),
            "osix.source_digest=" + encode(sourceDigest),
            "osix.lower=" + encode(lower),
            "osix.upper=" + encode(upper),
            "osix.work=" + encode(work),
            "osix.mode=" + encode(mode),
            "osix.rw=" + encode(rw ? "true" : "false")
        ].joined(separator: ",")

        try runProcess("/sbin/mount", ["-F", "-t", fsType, "-o", mountOptions, "osixfs", target])
    }

    static func readWriteOption(_ opts: [String: String]) throws -> Bool {
        guard let rawValue = opts["rw"] else {
            return true
        }
        switch rawValue.lowercased() {
        case "", "true":
            return true
        case "false":
            return false
        default:
            throw usage("--rw must be true or false")
        }
    }

    static func validateMountMode(_ mode: String) throws {
        guard mode == "overlay" || mode == "fuse" else {
            throw usage("unsupported --mode \(mode)")
        }
    }

    static func validateSourceDigest(_ digest: String) throws {
        let prefix = "sha256:"
        guard digest.hasPrefix(prefix) else {
            throw usage("--source-digest must be a sha256 digest")
        }
        let hex = digest.dropFirst(prefix.count)
        let hexDigits = CharacterSet(charactersIn: "0123456789abcdefABCDEF")
        guard hex.count == 64, hex.unicodeScalars.allSatisfy({ hexDigits.contains($0) }) else {
            throw usage("--source-digest must be a sha256 digest")
        }
    }

    static func validateMountPaths(target: String, workspaceRoot: String, lower: String, upper: String, work: String) throws {
        try validateMountTarget(path: target)
        try validateDirectory(path: workspaceRoot, option: "--workspace-root")
        try validateDirectory(path: lower, option: "--lower", rejectWorldWritable: true)
        try validateDirectory(path: upper, option: "--upper", rejectWorldWritable: true)
        try validateDirectory(path: work, option: "--work", rejectWorldWritable: true)
        try validateRuntimeDirectoriesAreDisjoint(lower: lower, upper: upper, work: work)
        try validateTargetIsDisjoint(target: target, lower: lower, upper: upper, work: work)
    }

    static func validateMountTarget(path: String) throws {
        var statBuffer = stat()
        if lstat(path, &statBuffer) == 0 {
            guard statBuffer.st_mode & S_IFMT == S_IFDIR else {
                throw CLIError(message: "--target \(path) is not a directory", code: 64)
            }
            return
        }
        guard errno == ENOENT else {
            throw CLIError(message: "--target \(path) is unavailable: \(String(cString: strerror(errno)))", code: 64)
        }
    }

    static func validateDirectory(path: String, option: String, rejectWorldWritable: Bool = false) throws {
        var statBuffer = stat()
        guard lstat(path, &statBuffer) == 0 else {
            throw CLIError(message: "\(option) \(path) is unavailable: \(String(cString: strerror(errno)))", code: 64)
        }
        guard statBuffer.st_mode & S_IFMT == S_IFDIR else {
            throw CLIError(message: "\(option) \(path) is not a directory", code: 64)
        }
        if rejectWorldWritable, statBuffer.st_mode & mode_t(S_IWOTH) != 0 {
            throw CLIError(message: "refusing world-writable runtime directory \(option) \(path)", code: 64)
        }
    }

    static func validateRuntimeDirectoriesAreDisjoint(lower: String, upper: String, work: String) throws {
        let directories = [
            ("--lower", canonicalPath(lower)),
            ("--upper", canonicalPath(upper)),
            ("--work", canonicalPath(work)),
        ]
        for index in directories.indices {
            for otherIndex in directories.indices where otherIndex > index {
                let current = directories[index]
                let other = directories[otherIndex]
                if current.1 == other.1 || isNestedPath(current.1, under: other.1) || isNestedPath(other.1, under: current.1) {
                    throw CLIError(message: "\(current.0) and \(other.0) must be separate directories", code: 64)
                }
            }
        }
    }

    static func validateTargetIsDisjoint(target: String, lower: String, upper: String, work: String) throws {
        let targetPath = canonicalPath(target)
        for directory in [
            ("--lower", canonicalPath(lower)),
            ("--upper", canonicalPath(upper)),
            ("--work", canonicalPath(work)),
        ] {
            if targetPath == directory.1 || isNestedPath(targetPath, under: directory.1) || isNestedPath(directory.1, under: targetPath) {
                throw CLIError(message: "--target and \(directory.0) must be separate directories", code: 64)
            }
        }
    }

    static func canonicalPath(_ path: String) -> String {
        URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
    }

    static func isNestedPath(_ path: String, under parent: String) -> Bool {
        path.hasPrefix(parent.hasSuffix("/") ? parent : parent + "/")
    }

    static func unmount(_ opts: [String: String]) throws {
        let target = try required(opts, "target")
        if opts.keys.contains("force") {
            try runProcess("/usr/sbin/diskutil", ["unmount", "force", target])
            return
        }
        try runProcess("/sbin/umount", [target])
    }

    static func requireReady(bundleID: String, fileSystemType: String? = nil) async throws {
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
                    message: "FSKit extension \(bundleID) is \(plugInKitState) but FSClient does not report it as enabled. PlugInKit registration is not FSKit runtime enablement; the public FSClient API only reports modules after FSKit enablement. Enable it in \(fileSystemExtensionSettingsPath).",
                    code: 69
                )
            }
            throw CLIError(message: "FSKit extension \(bundleID) is not installed or not discoverable", code: 69)
        }
        guard module.isEnabled else {
            let plugInKitState = (try? plugInKitRegistrationState(bundleID: bundleID)) ?? "installed"
            throw CLIError(
                message: "FSKit extension \(bundleID) is \(plugInKitState) but not enabled for FSKit. PlugInKit registration is not FSKit runtime enablement; enable it in \(fileSystemExtensionSettingsPath).",
                code: 69
            )
        }
        if let fileSystemType {
            try requireModule(module, supportsFileSystemType: fileSystemType)
        }
    }

    static func requireModule(_ module: FSModuleIdentity, supportsFileSystemType fileSystemType: String) throws {
        let names = try declaredFileSystemTypes(for: module)
        guard names.contains(fileSystemType) else {
            let declared = names.sorted().joined(separator: ", ")
            throw CLIError(
                message: "FSKit extension \(module.bundleIdentifier) is enabled but does not declare filesystem type \(fileSystemType); declared types: \(declared.isEmpty ? "none" : declared)",
                code: 69
            )
        }
    }

    static func declaredFileSystemTypes(for module: FSModuleIdentity) throws -> Set<String> {
        let infoURL = module.url.appendingPathComponent("Contents").appendingPathComponent("Info.plist")
        guard let info = NSDictionary(contentsOf: infoURL) as? [String: Any] else {
            throw CLIError(message: "failed to read FSKit extension Info.plist at \(infoURL.path)", code: 69)
        }
        guard let attributes = info["EXAppExtensionAttributes"] as? [String: Any] else {
            throw CLIError(message: "FSKit extension \(module.bundleIdentifier) is missing EXAppExtensionAttributes", code: 69)
        }
        var names = Set<String>()
        if let shortName = attributes["FSShortName"] as? String, !shortName.isEmpty {
            names.insert(shortName)
        }
        if let personalities = attributes["FSPersonalities"] as? [String: Any] {
            for value in personalities.values {
                guard let personality = value as? [String: Any] else {
                    continue
                }
                if let name = personality["FSName"] as? String, !name.isEmpty {
                    names.insert(name)
                }
            }
        }
        return names
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

func parseOptions(_ args: [String], allowed: Set<String>) throws -> [String: String] {
    let booleanOptions = Set(["force", "rw"])
    var opts: [String: String] = [:]
    var index = 0
    while index < args.count {
        let arg = args[index]
        if arg.hasPrefix("--") {
            let key = String(arg.dropFirst(2))
            guard allowed.contains(key) else {
                throw usage("unknown option --\(key)")
            }
            if index + 1 < args.count && !args[index + 1].hasPrefix("--") {
                opts[key] = args[index + 1]
                index += 2
            } else if booleanOptions.contains(key) {
                opts[key] = "true"
                index += 1
            } else {
                opts[key] = ""
                index += 1
            }
        } else {
            throw usage("unexpected argument \(arg)")
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

func optionOrEnvironment(_ opts: [String: String], _ key: String, envKey: String, fallback: String) throws -> String {
    if let value = opts[key] {
        guard !value.isEmpty else {
            throw usage("missing --\(key)")
        }
        return value
    }
    return environment(envKey, fallback)
}

func usage(_ message: String) -> CLIError {
    CLIError(message: "\(message)\nusage: osix-fskitctl doctor --bundle-id BUNDLE_ID [--fstype TYPE]\n       osix-fskitctl mount --target PATH --lower PATH --upper PATH --work PATH --source-ref REF --source-digest DIGEST --workspace-root PATH [--mode overlay|fuse] [--rw] [--fstype TYPE]\n       osix-fskitctl unmount --target PATH [--force]", code: 64)
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
