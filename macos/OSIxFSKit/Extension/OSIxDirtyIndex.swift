import CryptoKit
import Darwin
import Foundation

struct OSIxDirtyIndex {
    let dirtyBytes: Int64
    let paths: [String: String]
    let updatedAt: Date

    func write(to path: String) throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(Serializable(dirtyBytes: dirtyBytes, paths: paths, updatedAt: updatedAt))
        let directory = URL(fileURLWithPath: path).deletingLastPathComponent().path
        try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
        try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path)
    }

    static func rebuild(upper: String, parentTree: [String: OSIxTreeEntry] = [:]) -> OSIxDirtyIndex {
        var dirtyBytes: Int64 = 0
        var paths: [String: String] = [:]
        let fileManager = FileManager.default
        let upperURL = URL(fileURLWithPath: upper).resolvingSymlinksInPath().standardizedFileURL

        func visit(_ directory: URL, relativeBase: String) {
            guard let children = try? fileManager.contentsOfDirectory(
                at: directory,
                includingPropertiesForKeys: nil,
                options: []
            ) else {
                return
            }

            let whiteoutedNames = Set(children.compactMap { url -> String? in
                let name = url.lastPathComponent
                guard name.hasPrefix(".wh."), name != ".wh..wh..opq" else {
                    return nil
                }
                return String(name.dropFirst(".wh.".count))
            })

            for url in children {
                let path = url.standardizedFileURL.path
                let relativePath = joinRelative(relativeBase, url.lastPathComponent)
                guard !relativePath.isEmpty else {
                    continue
                }
                let name = url.lastPathComponent
                if name.hasPrefix(".wh."), name != ".wh..wh..opq" {
                    let target = joinRelative(parentPath(relativePath), String(name.dropFirst(".wh.".count)))
                    paths[target] = "deleted"
                    continue
                }
                if whiteoutedNames.contains(name) {
                    continue
                }
                guard let entry = treeEntry(path: path, relativePath: relativePath) else {
                    continue
                }
                let matchesParent = parentTree[relativePath] == entry
                if !matchesParent {
                    paths[relativePath] = "modified"
                    if entry.type == "file" {
                        dirtyBytes += entry.size ?? 0
                    }
                }
                if entry.type == "dir" {
                    visit(url, relativeBase: relativePath)
                }
            }
        }

        visit(upperURL, relativeBase: "")
        return OSIxDirtyIndex(dirtyBytes: dirtyBytes, paths: paths, updatedAt: Date())
    }

    static func parentTree(workspace: String?, sourceDigest: String?) -> [String: OSIxTreeEntry] {
        guard let workspace, let sourceDigest else {
            return [:]
        }
        do {
            let manifestData = try Data(contentsOf: blobURL(workspace: workspace, digest: sourceDigest))
            let manifest = try JSONDecoder().decode(OSIxManifest.self, from: manifestData)
            let configData = try Data(contentsOf: blobURL(workspace: workspace, digest: manifest.config.digest))
            let config = try JSONDecoder().decode(OSIxAgentConfig.self, from: configData)
            return Dictionary(uniqueKeysWithValues: config.tree.map { ($0.path, $0) })
        } catch {
            return [:]
        }
    }

    private static func treeEntry(path: String, relativePath: String) -> OSIxTreeEntry? {
        var statBuffer = stat()
        guard lstat(path, &statBuffer) == 0 else {
            return nil
        }
        let mode = Int64(statBuffer.st_mode & 0o777)
        let fileType = statBuffer.st_mode & S_IFMT
        if fileType == S_IFREG {
            guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else {
                return nil
            }
            return OSIxTreeEntry(path: relativePath, type: "file", mode: mode, size: Int64(statBuffer.st_size), digest: digest(data), linkname: nil)
        }
        if fileType == S_IFDIR {
            return OSIxTreeEntry(path: relativePath, type: "dir", mode: mode, size: nil, digest: nil, linkname: nil)
        }
        if fileType == S_IFLNK {
            var buffer = [CChar](repeating: 0, count: Int(MAXPATHLEN))
            let count = readlink(path, &buffer, buffer.count - 1)
            guard count >= 0 else {
                return nil
            }
            let destination = String(cString: Array(buffer[0..<count]) + [0])
            return OSIxTreeEntry(path: relativePath, type: "symlink", mode: mode, size: nil, digest: digest(Data(destination.utf8)), linkname: destination)
        }
        return nil
    }

    private static func blobURL(workspace: String, digest: String) -> URL {
        let hex = digest.replacingOccurrences(of: "sha256:", with: "")
        return URL(fileURLWithPath: workspace)
            .appendingPathComponent(".osix")
            .appendingPathComponent("blobs")
            .appendingPathComponent("sha256")
            .appendingPathComponent(hex)
    }

    private static func digest(_ data: Data) -> String {
        let hash = SHA256.hash(data: data)
        return "sha256:" + hash.map { String(format: "%02x", $0) }.joined()
    }

    private struct Serializable: Encodable {
        let dirtyBytes: Int64
        let paths: [String: String]
        let updatedAt: Date
    }
}

struct OSIxTreeEntry: Decodable, Equatable {
    let path: String
    let type: String
    let mode: Int64
    let size: Int64?
    let digest: String?
    let linkname: String?
}

private struct OSIxManifest: Decodable {
    let config: OSIxDescriptor
}

private struct OSIxDescriptor: Decodable {
    let digest: String
}

private struct OSIxAgentConfig: Decodable {
    let tree: [OSIxTreeEntry]
}
