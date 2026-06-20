import Darwin
import Foundation
import FSKit

private let opaqueWhiteoutName = ".wh..wh..opq"

@objc
final class OSIxVolume: FSVolume, FSVolume.Operations, FSVolume.ReadWriteOperations, FSVolume.XattrOperations {
    private let fileManager = FileManager.default
    private var mountOptions: OSIxMountOptions?
    private var root = OSIxItem.root

    convenience init(volumeID: FSVolume.Identifier, volumeName: FSFileName, mountOptions: OSIxMountOptions) {
        self.init(volumeID: volumeID, volumeName: volumeName)
        self.mountOptions = mountOptions
        self.root = resolveItem("") ?? .root
    }

    var supportedVolumeCapabilities: FSVolume.SupportedCapabilities {
        let capabilities = FSVolume.SupportedCapabilities()
        capabilities.supports64BitObjectIDs = true
        capabilities.supports2TBFiles = true
        capabilities.supportsFastStatFS = true
        capabilities.supportsSymbolicLinks = true
        capabilities.caseFormat = .sensitive
        return capabilities
    }

    var volumeStatistics: FSStatFSResult {
        let stats = FSStatFSResult(fileSystemTypeName: "OSIxFS")
        stats.blockSize = 4096
        stats.ioSize = 1024 * 1024
        stats.totalBytes = 1 << 40
        stats.freeBytes = 1 << 39
        stats.availableBytes = 1 << 39
        stats.totalFiles = 1_000_000
        stats.freeFiles = 999_999
        return stats
    }

    var maximumLinkCount: Int { 32_767 }
    var maximumNameLength: Int { 255 }
    var restrictsOwnershipChanges: Bool { true }
    var truncatesLongNames: Bool { false }
    var maximumXattrSize: Int { 128 * 1024 }
    var maximumFileSizeInBits: Int { 63 }

    func mount(options: FSTaskOptions, replyHandler reply: @escaping ((any Error)?) -> Void) {
        do {
            let parsedOptions = OSIxMountOptions.parse(options)
            try parsedOptions.validateForMount()
            mountOptions = parsedOptions
            root = resolveItem("") ?? .root
            reply(nil)
        } catch {
            reply(error)
        }
    }

    func unmount(replyHandler reply: @escaping () -> Void) {
        try? flushDirtyIndex()
        reply()
    }

    func synchronize(flags: FSSyncFlags, replyHandler reply: @escaping ((any Error)?) -> Void) {
        do {
            try flushDirtyIndex()
            reply(nil)
        } catch {
            reply(error)
        }
    }

    func getAttributes(_ desiredAttributes: FSItem.GetAttributesRequest, of item: FSItem, replyHandler reply: @escaping (FSItem.Attributes?, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(nil, posixError(ENOENT))
            return
        }
        do {
            reply(try attributes(for: try currentItem(for: item)), nil)
        } catch {
            reply(nil, error)
        }
    }

    func setAttributes(_ newAttributes: FSItem.SetAttributesRequest, on item: FSItem, replyHandler reply: @escaping (FSItem.Attributes?, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(nil, posixError(ENOENT))
            return
        }
        do {
            let current = try currentItem(for: item)
            if !wouldApplyAttributes(newAttributes, itemType: current.type) {
                reply(try attributes(for: current), nil)
                return
            }
            guard mountOptions?.upper != nil else {
                throw posixError(EINVAL)
            }
            let hadUpperItem = hasUpperItem(for: current.relativePath)
            let existingUpperParent = nearestExistingUpperParent(for: current.relativePath)
            let upperBackup = try backupUpperItem(current.relativePath)
            var changed = false
            do {
                changed = try applyAttributes(newAttributes, to: try ensureUpperItem(for: current), itemType: current.type)
            } catch {
                if !hadUpperItem {
                    removeCreatedUpperItemAndEmptyParents(current.relativePath, stoppingAt: existingUpperParent)
                }
                throw error
            }
            if changed {
                do {
                    try flushDirtyIndex()
                    try discardStashedHiddenUpperItem(upperBackup)
                } catch {
                    if let upperBackup {
                        try restoreStashedHiddenUpperItem(upperBackup)
                    } else if !hadUpperItem {
                        removeCreatedUpperItemAndEmptyParents(current.relativePath, stoppingAt: existingUpperParent)
                    }
                    throw error
                }
            } else {
                try discardStashedHiddenUpperItem(upperBackup)
            }
            reply(try attributes(for: try currentItem(for: item)), nil)
        } catch {
            reply(nil, error)
        }
    }

    func lookupItem(named name: FSFileName, inDirectory directory: FSItem, replyHandler reply: @escaping (FSItem?, FSFileName?, (any Error)?) -> Void) {
        guard let directory = directory as? OSIxItem else {
            reply(nil, nil, posixError(ENOENT))
            return
        }
        do {
            let currentDirectory = try currentDirectory(for: directory)
            let rawName = name.string ?? ""
            if rawName == "." || rawName.isEmpty {
                reply(currentDirectory, FSFileName(string: "."), nil)
                return
            }
            if rawName == ".." {
                reply(resolveItem(parentPath(currentDirectory.relativePath)) ?? root, FSFileName(string: ".."), nil)
                return
            }
            guard let rawName = validName(name) else {
                reply(nil, nil, posixError(EINVAL))
                return
            }
            let relativePath = joinRelative(currentDirectory.relativePath, rawName)
            guard let item = resolveItem(relativePath) else {
                reply(nil, nil, posixError(ENOENT))
                return
            }
            reply(item, FSFileName(string: rawName), nil)
        } catch {
            reply(nil, nil, error)
        }
    }

    func reclaimItem(_ item: FSItem, replyHandler reply: @escaping ((any Error)?) -> Void) {
        reply(nil)
    }

    func readSymbolicLink(_ item: FSItem, replyHandler reply: @escaping (FSFileName?, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(nil, posixError(ENOENT))
            return
        }
        do {
            let destination = try fileManager.destinationOfSymbolicLink(atPath: try currentItem(for: item).physicalPath)
            reply(FSFileName(string: destination), nil)
        } catch {
            reply(nil, error)
        }
    }

    func createItem(named name: FSFileName, type: FSItem.ItemType, inDirectory directory: FSItem, attributes newAttributes: FSItem.SetAttributesRequest, replyHandler reply: @escaping (FSItem?, FSFileName?, (any Error)?) -> Void) {
        guard let directory = directory as? OSIxItem,
              let rawName = validName(name),
              let upper = mountOptions?.upper else {
            reply(nil, nil, posixError(EINVAL))
            return
        }
        do {
            let currentDirectory = try currentDirectory(for: directory)
            let relativePath = joinRelative(currentDirectory.relativePath, rawName)
            let target = upperPath(upper, relativePath)
            let existingUpperParent = nearestExistingUpperParent(for: relativePath)
            guard type == .directory || type == .file else {
                throw posixError(ENOTSUP)
            }
            if resolveItem(relativePath) != nil {
                throw posixError(EEXIST)
            }
            try fileManager.createDirectory(atPath: parentFilesystemPath(target), withIntermediateDirectories: true)
            let stashedHiddenUpperItem = try stashHiddenUpperItemIfWhitedOut(relativePath)
            let whiteoutExisted = hasWhiteout(for: relativePath)
            var createdTarget = false
            var removedWhiteout = false
            do {
                switch type {
                case .directory:
                    try fileManager.createDirectory(atPath: target, withIntermediateDirectories: false)
                    createdTarget = true
                case .file:
                    if !fileManager.createFile(atPath: target, contents: Data()) {
                        throw posixError(EIO)
                    }
                    createdTarget = true
                default:
                    throw posixError(ENOTSUP)
                }
                _ = try applyAttributes(newAttributes, to: target, itemType: type)
                removeWhiteout(for: relativePath)
                removedWhiteout = whiteoutExisted
                let item = resolveItem(relativePath)
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(stashedHiddenUpperItem)
                reply(item, FSFileName(string: rawName), item == nil ? posixError(ENOENT) : nil)
            } catch {
                if createdTarget {
                    removeCreatedUpperItemAndEmptyParents(relativePath, stoppingAt: existingUpperParent)
                }
                if removedWhiteout {
                    try createWhiteout(for: relativePath)
                }
                try restoreStashedHiddenUpperItem(stashedHiddenUpperItem)
                throw error
            }
        } catch {
            reply(nil, nil, error)
        }
    }

    func createSymbolicLink(named name: FSFileName, inDirectory directory: FSItem, attributes newAttributes: FSItem.SetAttributesRequest, linkContents contents: FSFileName, replyHandler reply: @escaping (FSItem?, FSFileName?, (any Error)?) -> Void) {
        guard let directory = directory as? OSIxItem,
              let rawName = validName(name),
              let destination = contents.string,
              !destination.isEmpty,
              let upper = mountOptions?.upper else {
            reply(nil, nil, posixError(EINVAL))
            return
        }
        do {
            let currentDirectory = try currentDirectory(for: directory)
            let relativePath = joinRelative(currentDirectory.relativePath, rawName)
            let target = upperPath(upper, relativePath)
            let existingUpperParent = nearestExistingUpperParent(for: relativePath)
            if resolveItem(relativePath) != nil {
                throw posixError(EEXIST)
            }
            try fileManager.createDirectory(atPath: parentFilesystemPath(target), withIntermediateDirectories: true)
            let stashedHiddenUpperItem = try stashHiddenUpperItemIfWhitedOut(relativePath)
            let whiteoutExisted = hasWhiteout(for: relativePath)
            var createdTarget = false
            var removedWhiteout = false
            do {
                try fileManager.createSymbolicLink(atPath: target, withDestinationPath: destination)
                createdTarget = true
                _ = try applyAttributes(newAttributes, to: target, itemType: .symlink)
                removeWhiteout(for: relativePath)
                removedWhiteout = whiteoutExisted
                let item = resolveItem(relativePath)
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(stashedHiddenUpperItem)
                reply(item, FSFileName(string: rawName), item == nil ? posixError(ENOENT) : nil)
            } catch {
                if createdTarget {
                    removeCreatedUpperItemAndEmptyParents(relativePath, stoppingAt: existingUpperParent)
                }
                if removedWhiteout {
                    try createWhiteout(for: relativePath)
                }
                try restoreStashedHiddenUpperItem(stashedHiddenUpperItem)
                throw error
            }
        } catch {
            reply(nil, nil, error)
        }
    }

    func createLink(to item: FSItem, named name: FSFileName, inDirectory directory: FSItem, replyHandler reply: @escaping (FSFileName?, (any Error)?) -> Void) {
        reply(nil, posixError(ENOTSUP))
    }

    func getXattr(named name: FSFileName, of item: FSItem, replyHandler reply: @escaping (Data?, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem,
              let rawName = validXattrName(name) else {
            reply(nil, posixError(EINVAL))
            return
        }
        do {
            let current = try currentItem(for: item)
            let path = current.physicalPath
            let options = xattrOptions(for: current.type)
            let size = getxattr(path, rawName, nil, 0, 0, options)
            if size < 0 {
                throw posixError(errno)
            }
            guard size > 0 else {
                reply(Data(), nil)
                return
            }
            var buffer = [UInt8](repeating: 0, count: size)
            let readSize = getxattr(path, rawName, &buffer, buffer.count, 0, options)
            if readSize < 0 {
                throw posixError(errno)
            }
            reply(Data(buffer.prefix(readSize)), nil)
        } catch {
            reply(nil, error)
        }
    }

    func setXattr(named name: FSFileName, to value: Data?, on item: FSItem, policy: FSVolume.SetXattrPolicy, replyHandler reply: @escaping ((any Error)?) -> Void) {
        guard let item = item as? OSIxItem,
              let rawName = validXattrName(name) else {
            reply(posixError(EINVAL))
            return
        }
        do {
            let current = try currentItem(for: item)
            let options = xattrOptions(for: current.type)
            let hadUpperItem = hasUpperItem(for: current.relativePath)
            let existingUpperParent = nearestExistingUpperParent(for: current.relativePath)
            let upperBackup = try backupUpperItem(current.relativePath)
            var mutated = false
            do {
                switch policy {
                case .delete:
                    try requireXattrExists(rawName, item: current, options: options)
                    let path = try ensureUpperItem(for: current)
                    if removexattr(path, rawName, options) != 0 {
                        if errno != ENOATTR {
                            throw posixError(errno)
                        }
                    }
                    mutated = true
                case .alwaysSet, .mustCreate, .mustReplace:
                    let flags: Int32
                    switch policy {
                    case .mustCreate:
                        try requireXattrMissing(rawName, item: current, options: options)
                        flags = XATTR_CREATE
                    case .mustReplace:
                        try requireXattrExists(rawName, item: current, options: options)
                        flags = XATTR_REPLACE
                    default:
                        flags = 0
                    }
                    let path = try ensureUpperItem(for: current)
                    let data = value ?? Data()
                    let status = data.withUnsafeBytes { rawBuffer in
                        setxattr(path, rawName, rawBuffer.baseAddress, data.count, 0, options | flags)
                    }
                    if status != 0 {
                        if current.source == .lower, current.type == .directory, errno == ENOATTR, policy == .mustReplace {
                            let retryStatus = data.withUnsafeBytes { rawBuffer in
                                setxattr(path, rawName, rawBuffer.baseAddress, data.count, 0, options)
                            }
                            if retryStatus == 0 {
                                mutated = true
                                break
                            }
                        }
                        throw posixError(errno)
                    }
                    mutated = true
                @unknown default:
                    throw posixError(EINVAL)
                }
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(upperBackup)
            } catch {
                if mutated, let upperBackup {
                    try restoreStashedHiddenUpperItem(upperBackup)
                } else if !hadUpperItem {
                    removeCreatedUpperItemAndEmptyParents(current.relativePath, stoppingAt: existingUpperParent)
                } else {
                    try discardStashedHiddenUpperItem(upperBackup)
                }
                throw error
            }
            reply(nil)
        } catch {
            reply(error)
        }
    }

    private func requireXattrExists(_ name: String, item: OSIxItem, options: Int32) throws {
        if getxattr(item.physicalPath, name, nil, 0, 0, options) < 0 {
            throw posixError(errno)
        }
    }

    private func requireXattrMissing(_ name: String, item: OSIxItem, options: Int32) throws {
        if getxattr(item.physicalPath, name, nil, 0, 0, options) >= 0 {
            throw posixError(EEXIST)
        }
        guard errno == ENOATTR else {
            throw posixError(errno)
        }
    }

    func listXattrs(of item: FSItem, replyHandler reply: @escaping ([FSFileName]?, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(nil, posixError(EINVAL))
            return
        }
        do {
            let current = try currentItem(for: item)
            let path = current.physicalPath
            let options = xattrOptions(for: current.type)
            let size = listxattr(path, nil, 0, options)
            if size < 0 {
                throw posixError(errno)
            }
            guard size > 0 else {
                reply([], nil)
                return
            }
            var buffer = [CChar](repeating: 0, count: size)
            let readSize = listxattr(path, &buffer, buffer.count, options)
            if readSize < 0 {
                throw posixError(errno)
            }
            reply(parseXattrNames(buffer: buffer, count: readSize), nil)
        } catch {
            reply(nil, error)
        }
    }

    func removeItem(_ item: FSItem, named name: FSFileName, fromDirectory directory: FSItem, replyHandler reply: @escaping ((any Error)?) -> Void) {
        guard item is OSIxItem,
              let directory = directory as? OSIxItem,
              let rawName = validName(name),
              mountOptions?.upper != nil else {
            reply(posixError(EINVAL))
            return
        }
        do {
            let currentDirectory = try currentDirectory(for: directory)
            let relativePath = joinRelative(currentDirectory.relativePath, rawName)
            guard let current = resolveItem(relativePath) else {
                throw posixError(ENOENT)
            }
            if current.type == .directory {
                if try hasVisibleChildren(current) {
                    throw posixError(ENOTEMPTY)
                }
            }
            let coversLower = lowerItemExists(relativePath)
            let stashedUpperItem = try stashUpperItem(relativePath)
            var createdWhiteout = false
            do {
                if current.source == .lower || coversLower {
                    try createWhiteout(for: relativePath)
                    createdWhiteout = true
                }
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(stashedUpperItem)
                reply(nil)
            } catch {
                if createdWhiteout {
                    removeWhiteout(for: relativePath)
                }
                try restoreStashedHiddenUpperItem(stashedUpperItem)
                throw error
            }
        } catch {
            reply(error)
        }
    }

    func renameItem(_ item: FSItem, inDirectory sourceDirectory: FSItem, named sourceName: FSFileName, to destinationName: FSFileName, inDirectory destinationDirectory: FSItem, overItem: FSItem?, replyHandler reply: @escaping (FSFileName?, (any Error)?) -> Void) {
        guard item is OSIxItem,
              let sourceDirectory = sourceDirectory as? OSIxItem,
              let destinationDirectory = destinationDirectory as? OSIxItem,
              let oldName = validName(sourceName),
              let newName = validName(destinationName),
              let upper = mountOptions?.upper else {
            reply(nil, posixError(EINVAL))
            return
        }
        do {
            let currentSourceDirectory = try currentDirectory(for: sourceDirectory)
            let sourceRelativePath = joinRelative(currentSourceDirectory.relativePath, oldName)
            guard let current = resolveItem(sourceRelativePath) else {
                throw posixError(ENOENT)
            }
            let currentDestinationDirectory = try currentDirectory(for: destinationDirectory)
            let destinationRelativePath = joinRelative(currentDestinationDirectory.relativePath, newName)
            if sourceRelativePath == destinationRelativePath {
                reply(FSFileName(string: newName), nil)
                return
            }
            if current.type == .directory, isDescendantPath(destinationRelativePath, of: sourceRelativePath) {
                throw posixError(EINVAL)
            }
            if let destination = resolveItem(destinationRelativePath) {
                try validateRenameDestination(source: current, destination: destination)
            }
            let sourceCoversLower = lowerItemExists(sourceRelativePath)
            let destinationPath = upperPath(upper, destinationRelativePath)
            if current.type == .directory, sourceCoversLower {
                let existingDestinationParent = nearestExistingUpperParent(for: destinationRelativePath)
                let sourceUpperBackup = try backupUpperItem(sourceRelativePath)
                let stashedDestination = try stashHiddenUpperItemIfWhitedOut(destinationRelativePath)
                let destinationWhiteoutExisted = hasWhiteout(for: destinationRelativePath)
                var sourceUpperRemoved = false
                var createdSourceWhiteout = false
                var removedDestinationWhiteout = false
                do {
                    try fileManager.createDirectory(atPath: parentFilesystemPath(destinationPath), withIntermediateDirectories: true)
                    try renameLowerCoveringDirectory(current, destinationPath: destinationPath)
                    let sourceUpperPath = upperPath(upper, sourceRelativePath)
                    if itemExists(at: sourceUpperPath) {
                        try fileManager.removeItem(atPath: sourceUpperPath)
                        sourceUpperRemoved = true
                    }
                    try createWhiteout(for: sourceRelativePath)
                    createdSourceWhiteout = true
                    removeWhiteout(for: destinationRelativePath)
                    removedDestinationWhiteout = destinationWhiteoutExisted
                    try flushDirtyIndex()
                    try discardStashedHiddenUpperItem(sourceUpperBackup)
                    try discardStashedHiddenUpperItem(stashedDestination)
                    reply(FSFileName(string: newName), nil)
                    return
                } catch {
                    removeCreatedUpperItemAndEmptyParents(destinationRelativePath, stoppingAt: existingDestinationParent)
                    if createdSourceWhiteout {
                        removeWhiteout(for: sourceRelativePath)
                    }
                    if removedDestinationWhiteout {
                        try createWhiteout(for: destinationRelativePath)
                    }
                    if sourceUpperRemoved {
                        try restoreStashedHiddenUpperItem(sourceUpperBackup)
                    } else {
                        try discardStashedHiddenUpperItem(sourceUpperBackup)
                    }
                    try restoreStashedHiddenUpperItem(stashedDestination)
                    throw error
                }
            }
            let hadSourceUpperItem = hasUpperItem(for: sourceRelativePath)
            let existingSourceParent = nearestExistingUpperParent(for: sourceRelativePath)
            let sourcePath = try ensureUpperItem(for: current)
            try fileManager.createDirectory(atPath: parentFilesystemPath(destinationPath), withIntermediateDirectories: true)
            let stashedDestination = try stashHiddenUpperItemIfWhitedOut(destinationRelativePath)
            let destinationWhiteoutExisted = hasWhiteout(for: destinationRelativePath)
            let destinationBackup = stashedDestination == nil ? try backupUpperItem(destinationRelativePath) : nil
            if stashedDestination == nil, itemExists(at: destinationPath) {
                try fileManager.removeItem(atPath: destinationPath)
            }
            var movedSource = false
            var createdSourceWhiteout = false
            var removedDestinationWhiteout = false
            do {
                try fileManager.moveItem(atPath: sourcePath, toPath: destinationPath)
                movedSource = true
                if current.source == .lower || sourceCoversLower {
                    try createWhiteout(for: sourceRelativePath)
                    createdSourceWhiteout = true
                }
                removeWhiteout(for: destinationRelativePath)
                removedDestinationWhiteout = destinationWhiteoutExisted
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(destinationBackup)
                try discardStashedHiddenUpperItem(stashedDestination)
                reply(FSFileName(string: newName), nil)
            } catch {
                if createdSourceWhiteout {
                    removeWhiteout(for: sourceRelativePath)
                }
                if removedDestinationWhiteout {
                    try createWhiteout(for: destinationRelativePath)
                }
                if movedSource, itemExists(at: destinationPath) {
                    if itemExists(at: sourcePath) {
                        try? fileManager.removeItem(atPath: sourcePath)
                    }
                    try fileManager.moveItem(atPath: destinationPath, toPath: sourcePath)
                }
                if !hadSourceUpperItem {
                    removeCreatedUpperItemAndEmptyParents(sourceRelativePath, stoppingAt: existingSourceParent)
                }
                try restoreStashedHiddenUpperItem(destinationBackup)
                try restoreStashedHiddenUpperItem(stashedDestination)
                throw error
            }
        } catch {
            reply(nil, error)
        }
    }

    private func renameLowerCoveringDirectory(_ source: OSIxItem, destinationPath: String) throws {
        let temporaryDestination = destinationPath + ".osix-rename-" + UUID().uuidString
        do {
            try materializeVisibleDirectory(source, to: temporaryDestination)
            if itemExists(at: destinationPath) {
                try fileManager.removeItem(atPath: destinationPath)
            }
            try fileManager.moveItem(atPath: temporaryDestination, toPath: destinationPath)
        } catch {
            if itemExists(at: temporaryDestination) {
                try? fileManager.removeItem(atPath: temporaryDestination)
            }
            throw error
        }
    }

    private func materializeVisibleDirectory(_ directory: OSIxItem, to destination: String) throws {
        try fileManager.createDirectory(atPath: destination, withIntermediateDirectories: false)
        try copyDirectoryMetadata(from: directory.physicalPath, to: destination)
        for entry in try directoryEntries(for: directory) where entry.name != "." && entry.name != ".." {
            let child = try currentItem(for: entry.item)
            let childDestination = URL(fileURLWithPath: destination).appendingPathComponent(entry.name).path
            switch child.type {
            case .directory:
                try materializeVisibleDirectory(child, to: childDestination)
            default:
                try fileManager.copyItem(atPath: child.physicalPath, toPath: childDestination)
            }
        }
    }

    func enumerateDirectory(_ directory: FSItem, startingAt cookie: FSDirectoryCookie, verifier: FSDirectoryVerifier, attributes: FSItem.GetAttributesRequest?, packer: FSDirectoryEntryPacker, replyHandler reply: @escaping (FSDirectoryVerifier, (any Error)?) -> Void) {
        guard let directory = directory as? OSIxItem else {
            reply(verifier, posixError(ENOENT))
            return
        }
        do {
            let includeDotEntries = attributes == nil
            let entries = try directoryEntries(for: try currentDirectory(for: directory)).filter { entry in
                includeDotEntries || (entry.name != "." && entry.name != "..")
            }
            guard cookie.rawValue <= UInt64(Int.max) else {
                throw fsKitError(.invalidDirectoryCookie)
            }
            let start = Int(cookie.rawValue)
            guard start <= entries.count else {
                throw fsKitError(.invalidDirectoryCookie)
            }
            for index in start..<entries.count {
                let entry = entries[index]
                let item = entry.item
                let packed = packer.packEntry(
                    name: FSFileName(string: entry.name),
                    itemType: item.type,
                    itemID: item.id,
                    nextCookie: FSDirectoryCookie(rawValue: UInt64(index + 1)),
                    attributes: attributes == nil ? nil : try self.attributes(for: item)
                )
                if !packed {
                    break
                }
            }
            reply(FSDirectoryVerifier(rawValue: UInt64(entries.count + 1)), nil)
        } catch {
            reply(verifier, error)
        }
    }

    func activate(options: FSTaskOptions, replyHandler reply: @escaping (FSItem?, (any Error)?) -> Void) {
        root = resolveItem("") ?? .root
        reply(root, nil)
    }

    func deactivate(options: FSDeactivateOptions, replyHandler reply: @escaping ((any Error)?) -> Void) {
        do {
            try flushDirtyIndex()
            reply(nil)
        } catch {
            reply(error)
        }
    }

    func read(from item: FSItem, at offset: off_t, length: Int, into buffer: FSMutableFileDataBuffer, replyHandler reply: @escaping (Int, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(0, posixError(EINVAL))
            return
        }
        guard offset >= 0, length >= 0 else {
            reply(0, posixError(EINVAL))
            return
        }
        do {
            let current = try currentItem(for: item)
            guard current.type == .file else {
                throw posixError(current.type == .directory ? EISDIR : EINVAL)
            }
            let handle = try FileHandle(forReadingFrom: URL(fileURLWithPath: current.physicalPath))
            var handleClosed = false
            defer {
                if !handleClosed {
                    try? handle.close()
                }
            }
            try handle.seek(toOffset: UInt64(offset))
            let data = try handle.read(upToCount: length) ?? Data()
            try handle.close()
            handleClosed = true
            let rawBuffer = unsafeBitCast(buffer, to: OSIxMutableFileDataBuffer.self).mutableBytes()
            data.copyBytes(to: rawBuffer.assumingMemoryBound(to: UInt8.self), count: data.count)
            reply(data.count, nil)
        } catch {
            reply(0, error)
        }
    }

    func write(contents: Data, to item: FSItem, at offset: off_t, replyHandler reply: @escaping (Int, (any Error)?) -> Void) {
        guard let item = item as? OSIxItem else {
            reply(0, posixError(EINVAL))
            return
        }
        guard offset >= 0 else {
            reply(0, posixError(EINVAL))
            return
        }
        do {
            let current = try currentItem(for: item)
            guard current.type == .file else {
                throw posixError(current.type == .directory ? EISDIR : EINVAL)
            }
            let existingUpperParent = nearestExistingUpperParent(for: current.relativePath)
            let upperBackup = try backupUpperItem(current.relativePath)
            var preparedUpperFile = false
            do {
                let path = try ensureUpperFile(for: current)
                preparedUpperFile = true
                let handle = try FileHandle(forWritingTo: URL(fileURLWithPath: path))
                var handleClosed = false
                defer {
                    if !handleClosed {
                        try? handle.close()
                    }
                }
                try handle.seek(toOffset: UInt64(offset))
                try handle.write(contentsOf: contents)
                try handle.close()
                handleClosed = true
                try flushDirtyIndex()
                try discardStashedHiddenUpperItem(upperBackup)
                reply(contents.count, nil)
            } catch {
                if preparedUpperFile {
                    if upperBackup != nil {
                        try restoreStashedHiddenUpperItem(upperBackup)
                    } else {
                        removeCreatedUpperItemAndEmptyParents(current.relativePath, stoppingAt: existingUpperParent)
                    }
                }
                throw error
            }
        } catch {
            reply(0, error)
        }
    }

    private func attributes(for item: OSIxItem) throws -> FSItem.Attributes {
        let attributes = FSItem.Attributes()
        var statBuffer = stat()
        guard lstat(item.physicalPath, &statBuffer) == 0 else {
            throw posixError(errno)
        }
        attributes.type = item.type
        attributes.mode = UInt32(statBuffer.st_mode & 0o7777)
        attributes.linkCount = UInt32(statBuffer.st_nlink)
        attributes.uid = UInt32(statBuffer.st_uid)
        attributes.gid = UInt32(statBuffer.st_gid)
        attributes.size = UInt64(max(statBuffer.st_size, 0))
        attributes.allocSize = attributes.size
        attributes.fileID = item.id
        attributes.parentID = item.parentID
        attributes.accessTime = statBuffer.st_atimespec
        attributes.modifyTime = statBuffer.st_mtimespec
        attributes.changeTime = statBuffer.st_ctimespec
        attributes.birthTime = statBuffer.st_birthtimespec
        return attributes
    }

    private func currentItem(for item: OSIxItem) throws -> OSIxItem {
        guard let current = resolveItem(item.relativePath) else {
            throw posixError(ENOENT)
        }
        return current
    }

    private func currentDirectory(for item: OSIxItem) throws -> OSIxItem {
        let current = try currentItem(for: item)
        guard current.type == .directory else {
            throw posixError(ENOTDIR)
        }
        return current
    }

    private func applyAttributes(_ newAttributes: FSItem.SetAttributesRequest, to path: String, itemType: FSItem.ItemType) throws -> Bool {
        var changed = false
        if newAttributes.isValid(.size), itemType == .file {
            let handle = try FileHandle(forWritingTo: URL(fileURLWithPath: path))
            var handleClosed = false
            defer {
                if !handleClosed {
                    try? handle.close()
                }
            }
            try handle.truncate(atOffset: newAttributes.size)
            try handle.close()
            handleClosed = true
            newAttributes.consumedAttributes.insert(.size)
            changed = true
        }
        if newAttributes.isValid(.mode), itemType != .symlink {
            if chmod(path, mode_t(newAttributes.mode & 0o7777)) != 0 {
                throw posixError(errno)
            }
            newAttributes.consumedAttributes.insert(.mode)
            changed = true
        }
        if (newAttributes.isValid(.uid) || newAttributes.isValid(.gid)), itemType != .symlink {
            let uid = newAttributes.isValid(.uid) ? uid_t(newAttributes.uid) : uid_t.max
            let gid = newAttributes.isValid(.gid) ? gid_t(newAttributes.gid) : gid_t.max
            if chown(path, uid, gid) != 0 {
                throw posixError(errno)
            }
            if newAttributes.isValid(.uid) {
                newAttributes.consumedAttributes.insert(.uid)
            }
            if newAttributes.isValid(.gid) {
                newAttributes.consumedAttributes.insert(.gid)
            }
            changed = true
        }
        if newAttributes.isValid(.accessTime) || newAttributes.isValid(.modifyTime) {
            var times = [
                timespec(tv_sec: 0, tv_nsec: Int(UTIME_OMIT)),
                timespec(tv_sec: 0, tv_nsec: Int(UTIME_OMIT)),
            ]
            if newAttributes.isValid(.accessTime) {
                times[0] = newAttributes.accessTime
                newAttributes.consumedAttributes.insert(.accessTime)
            }
            if newAttributes.isValid(.modifyTime) {
                times[1] = newAttributes.modifyTime
                newAttributes.consumedAttributes.insert(.modifyTime)
            }
            if utimensat(AT_FDCWD, path, &times, itemType == .symlink ? AT_SYMLINK_NOFOLLOW : 0) != 0 {
                throw posixError(errno)
            }
            changed = true
        }
        return changed
    }

    private func wouldApplyAttributes(_ newAttributes: FSItem.SetAttributesRequest, itemType: FSItem.ItemType) -> Bool {
        if newAttributes.isValid(.size), itemType == .file {
            return true
        }
        if newAttributes.isValid(.mode), itemType != .symlink {
            return true
        }
        if (newAttributes.isValid(.uid) || newAttributes.isValid(.gid)), itemType != .symlink {
            return true
        }
        return newAttributes.isValid(.accessTime) || newAttributes.isValid(.modifyTime)
    }

    private func resolveItem(_ relativePath: String) -> OSIxItem? {
        let normalized = normalizeRelativePath(relativePath)
        if isWhitedOut(normalized) {
            return nil
        }
        if let upper = mountOptions?.upper {
            let upperCandidate = upperPath(upper, normalized)
            if let type = itemType(at: upperCandidate) {
                return OSIxItem(relativePath: normalized, physicalPath: upperCandidate, type: type, source: .upper)
            }
        }
        if let lower = mountOptions?.lower {
            let lowerCandidate = upperPath(lower, normalized)
            if let type = itemType(at: lowerCandidate), !isCoveredByOpaqueUpperDirectory(normalized) {
                return OSIxItem(relativePath: normalized, physicalPath: lowerCandidate, type: type, source: .lower)
            }
        }
        if normalized.isEmpty {
            return .root
        }
        return nil
    }

    private func directoryEntries(for directory: OSIxItem) throws -> [(name: String, item: OSIxItem)] {
        var names = Set<String>()
        if let lower = mountOptions?.lower {
            names.formUnion(try visibleNames(in: upperPath(lower, directory.relativePath), applyingWhiteouts: false))
        }
        if let upper = mountOptions?.upper {
            names.formUnion(try visibleNames(in: upperPath(upper, directory.relativePath), applyingWhiteouts: true))
        }
        var entries: [(String, OSIxItem)] = []
        entries.append((".", directory))
        entries.append(("..", resolveItem(parentPath(directory.relativePath)) ?? root))
        for name in names.sorted() {
            guard let item = resolveItem(joinRelative(directory.relativePath, name)) else {
                continue
            }
            entries.append((name, item))
        }
        return entries
    }

    private func visibleNames(in path: String, applyingWhiteouts: Bool) throws -> Set<String> {
        guard fileManager.fileExists(atPath: path) else {
            return []
        }
        var names = Set(try fileManager.contentsOfDirectory(atPath: path))
        if applyingWhiteouts {
            for name in names where name.hasPrefix(".wh.") {
                names.remove(name)
                if name != opaqueWhiteoutName {
                    names.remove(String(name.dropFirst(".wh.".count)))
                }
            }
        } else if let upper = mountOptions?.upper {
            let relative = relativePathFromRoot(path, root: mountOptions?.lower ?? "")
            let upperDir = upperPath(upper, relative)
            if fileManager.fileExists(atPath: upperPath(upperDir, opaqueWhiteoutName)) {
                return []
            }
            let whiteouts = try? fileManager.contentsOfDirectory(atPath: upperDir).filter { $0.hasPrefix(".wh.") }
            for whiteout in whiteouts ?? [] {
                if whiteout != opaqueWhiteoutName {
                    names.remove(String(whiteout.dropFirst(".wh.".count)))
                }
            }
        }
        names.remove(".")
        names.remove("..")
        return names
    }

    private func ensureUpperFile(for item: OSIxItem) throws -> String {
        let path = try ensureUpperItem(for: item)
        guard itemType(at: path) == .file else {
            throw posixError(EISDIR)
        }
        return path
    }

    private func ensureUpperItem(for item: OSIxItem) throws -> String {
        guard let upper = mountOptions?.upper else {
            throw posixError(EINVAL)
        }
        guard let current = resolveItem(item.relativePath) else {
            throw posixError(ENOENT)
        }
        let target = upperPath(upper, item.relativePath)
        if itemExists(at: target) {
            return target
        }
        guard current.source == .lower else {
            throw posixError(ENOENT)
        }
        let existingUpperParent = nearestExistingUpperParent(for: item.relativePath)
        do {
            try fileManager.createDirectory(atPath: parentFilesystemPath(target), withIntermediateDirectories: true)
            if current.type == .directory {
                try fileManager.createDirectory(atPath: target, withIntermediateDirectories: false)
                try copyDirectoryMetadata(from: current.physicalPath, to: target)
                return target
            }
            try fileManager.copyItem(atPath: current.physicalPath, toPath: target)
            return target
        } catch {
            removeCreatedUpperItemAndEmptyParents(item.relativePath, stoppingAt: existingUpperParent)
            throw error
        }
    }

    private func hasUpperItem(for relativePath: String) -> Bool {
        guard let upper = mountOptions?.upper else {
            return false
        }
        return itemExists(at: upperPath(upper, relativePath))
    }

    private func nearestExistingUpperParent(for relativePath: String) -> String? {
        var parent = parentPath(relativePath)
        while !parent.isEmpty {
            if hasUpperItem(for: parent) {
                return parent
            }
            parent = parentPath(parent)
        }
        return nil
    }

    private func removeCreatedUpperItemAndEmptyParents(_ relativePath: String, stoppingAt existingParent: String?) {
        guard let upper = mountOptions?.upper else {
            return
        }
        let target = upperPath(upper, relativePath)
        if itemExists(at: target) {
            try? fileManager.removeItem(atPath: target)
        }
        var parent = parentPath(relativePath)
        while !parent.isEmpty {
            if parent == existingParent {
                break
            }
            let parentTarget = upperPath(upper, parent)
            guard let children = try? fileManager.contentsOfDirectory(atPath: parentTarget), children.isEmpty else {
                break
            }
            try? fileManager.removeItem(atPath: parentTarget)
            parent = parentPath(parent)
        }
    }

    private func copyDirectoryMetadata(from source: String, to destination: String) throws {
        var statBuffer = stat()
        guard lstat(source, &statBuffer) == 0 else {
            throw posixError(errno)
        }
        if chmod(destination, statBuffer.st_mode & 0o7777) != 0 {
            throw posixError(errno)
        }
        var times = [
            statBuffer.st_atimespec,
            statBuffer.st_mtimespec,
        ]
        if utimensat(AT_FDCWD, destination, &times, 0) != 0 {
            throw posixError(errno)
        }
    }

    private func createWhiteout(for relativePath: String) throws {
        guard let upper = mountOptions?.upper else {
            throw posixError(EINVAL)
        }
        let whiteout = whiteoutPath(upper: upper, relativePath: relativePath)
        try fileManager.createDirectory(atPath: parentFilesystemPath(whiteout), withIntermediateDirectories: true)
        if !fileManager.createFile(atPath: whiteout, contents: Data()) {
            throw posixError(EIO)
        }
    }

    private func flushDirtyIndex() throws {
        guard let upper = mountOptions?.upper,
              let work = mountOptions?.work else {
            return
        }
        let dirtyPath = URL(fileURLWithPath: work)
            .deletingLastPathComponent()
            .appendingPathComponent("dirty.json")
            .path
        let parentTree = try OSIxDirtyIndex.parentTree(workspace: mountOptions?.workspace, sourceDigest: mountOptions?.sourceDigest)
        try OSIxDirtyIndex.rebuild(upper: upper, parentTree: parentTree).write(to: dirtyPath)
    }

    private func removeWhiteout(for relativePath: String) {
        guard let upper = mountOptions?.upper else {
            return
        }
        let whiteout = whiteoutPath(upper: upper, relativePath: relativePath)
        if fileManager.fileExists(atPath: whiteout) {
            try? fileManager.removeItem(atPath: whiteout)
        }
    }

    private func hasWhiteout(for relativePath: String) -> Bool {
        guard let upper = mountOptions?.upper else {
            return false
        }
        return fileManager.fileExists(atPath: whiteoutPath(upper: upper, relativePath: relativePath))
    }

    private struct StashedHiddenUpperItem {
        let originalPath: String
        let stashedPath: String
    }

    private func stashHiddenUpperItemIfWhitedOut(_ relativePath: String) throws -> StashedHiddenUpperItem? {
        guard let upper = mountOptions?.upper else {
            return nil
        }
        let whiteout = whiteoutPath(upper: upper, relativePath: relativePath)
        guard fileManager.fileExists(atPath: whiteout) else {
            return nil
        }
        let target = upperPath(upper, relativePath)
        guard itemExists(at: target) else {
            return nil
        }
        let stashedPath = target + ".osix-hidden-" + UUID().uuidString
        try fileManager.moveItem(atPath: target, toPath: stashedPath)
        return StashedHiddenUpperItem(originalPath: target, stashedPath: stashedPath)
    }

    private func stashUpperItem(_ relativePath: String) throws -> StashedHiddenUpperItem? {
        guard let upper = mountOptions?.upper else {
            return nil
        }
        let target = upperPath(upper, relativePath)
        guard itemExists(at: target) else {
            return nil
        }
        let stashedPath = target + ".osix-stash-" + UUID().uuidString
        try fileManager.moveItem(atPath: target, toPath: stashedPath)
        return StashedHiddenUpperItem(originalPath: target, stashedPath: stashedPath)
    }

    private func backupUpperItem(_ relativePath: String) throws -> StashedHiddenUpperItem? {
        guard let upper = mountOptions?.upper else {
            return nil
        }
        let target = upperPath(upper, relativePath)
        guard itemExists(at: target) else {
            return nil
        }
        let stashedPath = target + ".osix-backup-" + UUID().uuidString
        try fileManager.copyItem(atPath: target, toPath: stashedPath)
        return StashedHiddenUpperItem(originalPath: target, stashedPath: stashedPath)
    }

    private func discardStashedHiddenUpperItem(_ item: StashedHiddenUpperItem?) throws {
        guard let item, itemExists(at: item.stashedPath) else {
            return
        }
        try fileManager.removeItem(atPath: item.stashedPath)
    }

    private func restoreStashedHiddenUpperItem(_ item: StashedHiddenUpperItem?) throws {
        guard let item, itemExists(at: item.stashedPath) else {
            return
        }
        if itemExists(at: item.originalPath) {
            try fileManager.removeItem(atPath: item.originalPath)
        }
        try fileManager.moveItem(atPath: item.stashedPath, toPath: item.originalPath)
    }

    private func lowerItemExists(_ relativePath: String) -> Bool {
        guard let lower = mountOptions?.lower else {
            return false
        }
        return itemType(at: upperPath(lower, relativePath)) != nil
    }

    private func itemExists(at path: String) -> Bool {
        itemType(at: path) != nil
    }

    private func hasVisibleChildren(_ directory: OSIxItem) throws -> Bool {
        for entry in try directoryEntries(for: directory) where entry.name != "." && entry.name != ".." {
            return true
        }
        return false
    }

    private func validateRenameDestination(source: OSIxItem, destination: OSIxItem) throws {
        if source.type == .directory {
            guard destination.type == .directory else {
                throw posixError(ENOTDIR)
            }
            if try hasVisibleChildren(destination) {
                throw posixError(ENOTEMPTY)
            }
            return
        }
        if destination.type == .directory {
            throw posixError(EISDIR)
        }
    }

    private func xattrOptions(for itemType: FSItem.ItemType) -> Int32 {
        itemType == .symlink ? XATTR_NOFOLLOW : 0
    }

    private func isWhitedOut(_ relativePath: String) -> Bool {
        let normalized = normalizeRelativePath(relativePath)
        guard !normalized.isEmpty, let upper = mountOptions?.upper else {
            return false
        }
        var candidate = normalized
        while !candidate.isEmpty {
            if fileManager.fileExists(atPath: whiteoutPath(upper: upper, relativePath: candidate)) {
                return true
            }
            candidate = parentPath(candidate)
        }
        return false
    }

    private func isCoveredByOpaqueUpperDirectory(_ relativePath: String) -> Bool {
        let normalized = normalizeRelativePath(relativePath)
        guard !normalized.isEmpty, let upper = mountOptions?.upper else {
            return false
        }
        var candidate = parentPath(normalized)
        while true {
            if fileManager.fileExists(atPath: upperPath(upperPath(upper, candidate), opaqueWhiteoutName)) {
                return true
            }
            if candidate.isEmpty {
                break
            }
            candidate = parentPath(candidate)
        }
        return false
    }

    private func itemType(at path: String) -> FSItem.ItemType? {
        var statBuffer = stat()
        guard lstat(path, &statBuffer) == 0 else {
            return nil
        }
        let fileType = statBuffer.st_mode & S_IFMT
        if fileType == S_IFDIR {
            return .directory
        }
        if fileType == S_IFLNK {
            return .symlink
        }
        if fileType == S_IFREG {
            return .file
        }
        return nil
    }

    private func validName(_ name: FSFileName) -> String? {
        guard let string = name.string,
              !string.isEmpty,
              string != ".",
              string != "..",
              !string.contains("/"),
              !string.contains("\0"),
              !string.hasPrefix(".wh.") else {
            return nil
        }
        return string
    }

    private func validXattrName(_ name: FSFileName) -> String? {
        guard let string = name.string,
              !string.isEmpty,
              !string.contains("/"),
              !string.contains("\0") else {
            return nil
        }
        return string
    }
}

private func parseXattrNames(buffer: [CChar], count: Int) -> [FSFileName] {
    var names: [FSFileName] = []
    var start = 0
    for index in 0..<count where buffer[index] == 0 {
        if index > start {
            names.append(FSFileName(string: String(cString: Array(buffer[start...index]))))
        }
        start = index + 1
    }
    return names
}

final class OSIxItem: FSItem {
    enum Source: Equatable {
        case upper
        case lower
        case synthetic
    }

    static let root = OSIxItem(relativePath: "", physicalPath: "/", type: .directory, source: .synthetic)

    let relativePath: String
    let physicalPath: String
    let type: FSItem.ItemType
    let source: Source
    let id: FSItem.Identifier
    let parentID: FSItem.Identifier

    init(relativePath: String, physicalPath: String, type: FSItem.ItemType, source: Source) {
        self.relativePath = normalizeRelativePath(relativePath)
        self.physicalPath = physicalPath
        self.type = type
        self.source = source
        self.id = itemID(for: self.relativePath)
        self.parentID = parentItemID(for: self.relativePath)
        super.init()
    }
}

func joinRelative(_ parent: String, _ child: String) -> String {
    let parent = normalizeRelativePath(parent)
    let child = normalizeRelativePath(child)
    return parent.isEmpty ? child : parent + "/" + child
}

func normalizeRelativePath(_ path: String) -> String {
    path.split(separator: "/").filter { $0 != "." && $0 != ".." }.joined(separator: "/")
}

func parentPath(_ path: String) -> String {
    let normalized = normalizeRelativePath(path)
    guard let slash = normalized.lastIndex(of: "/") else {
        return ""
    }
    return String(normalized[..<slash])
}

private func isDescendantPath(_ path: String, of ancestor: String) -> Bool {
    let path = normalizeRelativePath(path)
    let ancestor = normalizeRelativePath(ancestor)
    guard !ancestor.isEmpty else {
        return !path.isEmpty
    }
    return path.hasPrefix(ancestor + "/")
}

private func parentFilesystemPath(_ path: String) -> String {
    URL(fileURLWithPath: path).deletingLastPathComponent().path
}

private func upperPath(_ root: String, _ relativePath: String) -> String {
    let normalized = normalizeRelativePath(relativePath)
    return normalized.isEmpty ? root : URL(fileURLWithPath: root).appendingPathComponent(normalized).path
}

private func whiteoutPath(upper: String, relativePath: String) -> String {
    let normalized = normalizeRelativePath(relativePath)
    let name = URL(fileURLWithPath: normalized).lastPathComponent
    let parent = parentPath(normalized)
    return upperPath(upper, joinRelative(parent, ".wh." + name))
}

private func relativePathFromRoot(_ path: String, root: String) -> String {
    guard !root.isEmpty, path.hasPrefix(root) else {
        return ""
    }
    return normalizeRelativePath(String(path.dropFirst(root.count)))
}

private func itemID(for relativePath: String) -> FSItem.Identifier {
    let normalized = normalizeRelativePath(relativePath)
    if normalized.isEmpty {
        return .rootDirectory
    }
    var hash: UInt64 = 14_695_981_039_346_656_037
    for byte in normalized.utf8 {
        hash ^= UInt64(byte)
        hash &*= 1_099_511_628_211
    }
    return FSItem.Identifier(rawValue: max(hash, 3)) ?? .rootDirectory
}

private func parentItemID(for relativePath: String) -> FSItem.Identifier {
    let parent = parentPath(relativePath)
    return parent.isEmpty ? .rootDirectory : itemID(for: parent)
}

private func timespec(_ date: Date) -> timespec {
    let seconds = date.timeIntervalSince1970
    let wholeSeconds = floor(seconds)
    return Darwin.timespec(tv_sec: Int(wholeSeconds), tv_nsec: Int((seconds - wholeSeconds) * 1_000_000_000))
}

private func posixError(_ code: Int32) -> NSError {
    NSError(domain: NSPOSIXErrorDomain, code: Int(code))
}

private func fsKitError(_ code: FSError.Code) -> NSError {
    NSError(domain: FSKitErrorDomain, code: Int(code.rawValue))
}

@objc
private protocol OSIxMutableFileDataBuffer {
    func mutableBytes() -> UnsafeMutableRawPointer
}
