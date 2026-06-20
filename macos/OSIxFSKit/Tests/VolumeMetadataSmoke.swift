import Darwin
import Foundation
import FSKit

@main
struct VolumeMetadataSmoke {
    static func main() throws {
        guard CommandLine.arguments.count == 4 else {
            fputs("usage: VolumeMetadataSmoke LOWER_DIR UPPER_DIR WORK_DIR\n", stderr)
            Foundation.exit(64)
        }

        let lower = CommandLine.arguments[1]
        let upper = CommandLine.arguments[2]
        let work = CommandLine.arguments[3]
        let relativePath = "agent/workspace/file.txt"
        let staleDeletePath = "agent/workspace/delete-me.txt"
        let staleRenamePath = "agent/workspace/rename-me.txt"
        let staleRenameDestinationPath = "agent/workspace/renamed.txt"
        let nameBoundaryRemovePath = "agent/workspace/name-boundary-remove.txt"
        let nameBoundaryRenamePath = "agent/workspace/name-boundary-rename.txt"
        let nameBoundaryRenameDestinationPath = "agent/workspace/name-boundary-renamed.txt"
        let renameOverLowerFileSourcePath = "agent/workspace/rename-over-lower-file-source.txt"
        let renameOverLowerFileDestinationPath = "agent/workspace/rename-over-lower-file-dest.txt"
        let failedAttributesPath = "agent/workspace/failed-attrs.txt"
        let cowDeletePath = "agent/workspace/cow-delete.txt"
        let removeFlushRollbackPath = "agent/workspace/remove-flush-rollback.txt"
        let cowRenamePath = "agent/workspace/cow-rename.txt"
        let cowRenameDestinationPath = "agent/workspace/cow-renamed.txt"
        let renameHiddenRollbackSourcePath = "agent/workspace/rename-hidden-rollback-source.txt"
        let renameHiddenRollbackDestinationPath = "agent/workspace/rename-hidden-rollback-dest.txt"
        let writePath = "agent/workspace/write-me.txt"
        let absentXattrPath = "agent/workspace/absent-xattr.txt"
        let copyFailurePath = "agent/copy-failure/unreadable.txt"
        let copyFailureRenameDestinationDirectoryPath = "agent/copy-failure-rename-dest"
        let copyFailureRenameDestinationPath = "agent/copy-failure-rename-dest/renamed.txt"
        let oversizedXattrPath = "agent/xattr-rollback/too-large.txt"
        let existingParentXattrPath = "agent/existing-upper-parent/too-long-name.txt"
        let replaceWhiteoutPath = "agent/workspace/replace-whiteout.txt"
        let replaceHiddenWhiteoutPath = "agent/workspace/replace-hidden-whiteout.txt"
        let rollbackWhiteoutPath = "agent/workspace/rollback-whiteout.txt"
        let rollbackHiddenWhiteoutPath = "agent/workspace/rollback-hidden-whiteout.txt"
        let createFlushRollbackHiddenWhiteoutPath = "agent/workspace/create-flush-rollback-hidden-whiteout.txt"
        let selfRenamePath = "agent/workspace/self-rename.txt"
        let renameOverLowerDirectorySourcePath = "agent/workspace/rename-over-lower-dir-source.txt"
        let renameOverLowerDirectoryDestinationPath = "agent/workspace/rename-over-lower-dir-dest"
        let renameOverUpperDirectorySourcePath = "agent/workspace/rename-over-upper-dir-source.txt"
        let renameOverUpperDirectoryDestinationPath = "agent/workspace/rename-over-upper-dir-dest"
        let renameIntoRemovedDirectoryPath = "agent/workspace/rename-into-removed.txt"
        let renameLowerDirectoryFlushRollbackPath = "agent/workspace/rename-lower-dir-flush-rollback"
        let renameLowerDirectoryFlushRollbackDestinationPath = "agent/workspace/renamed-lower-dir-flush-rollback"
        let renameLowerDirectoryPath = "agent/workspace/rename-lower-dir"
        let renameLowerDirectoryDestinationPath = "agent/workspace/renamed-lower-dir"
        let renameIntoSelfDirectoryPath = "agent/workspace/rename-into-self"
        let ignoredDirectoryPath = "agent/cache"
        let unsupportedCreateDirectoryPath = "agent/unsupported-create-dir"
        let emptySymlinkDirectoryPath = "agent/empty-symlink-dir"
        let staleTypeDirectoryPath = "agent/stale-type-dir"
        let removedDirectoryPath = "agent/removed"
        let nonEmptyLowerDirectoryPath = "agent/non-empty-lower"
        let nonEmptyUpperDirectoryPath = "agent/non-empty-upper"
        let xattrDirectoryPath = "agent/xattr-dir"
        let danglingLinkPath = "agent/workspace/dangling-link"
        let upperDanglingRemovePath = "agent/workspace/upper-dangling-remove"
        let upperDanglingRenamePath = "agent/workspace/upper-dangling-rename"
        let upperDanglingRenameDestinationPath = "agent/workspace/upper-dangling-renamed"
        let lowerFile = URL(fileURLWithPath: lower).appendingPathComponent(relativePath).path
        let lowerDeleteFile = URL(fileURLWithPath: lower).appendingPathComponent(staleDeletePath).path
        let lowerRenameFile = URL(fileURLWithPath: lower).appendingPathComponent(staleRenamePath).path
        let lowerNameBoundaryRemoveFile = URL(fileURLWithPath: lower).appendingPathComponent(nameBoundaryRemovePath).path
        let lowerNameBoundaryRenameFile = URL(fileURLWithPath: lower).appendingPathComponent(nameBoundaryRenamePath).path
        let lowerRenameOverLowerFileSource = URL(fileURLWithPath: lower).appendingPathComponent(renameOverLowerFileSourcePath).path
        let lowerRenameOverLowerFileDestination = URL(fileURLWithPath: lower).appendingPathComponent(renameOverLowerFileDestinationPath).path
        let lowerFailedAttributesFile = URL(fileURLWithPath: lower).appendingPathComponent(failedAttributesPath).path
        let lowerCOWDeleteFile = URL(fileURLWithPath: lower).appendingPathComponent(cowDeletePath).path
        let lowerRemoveFlushRollbackFile = URL(fileURLWithPath: lower).appendingPathComponent(removeFlushRollbackPath).path
        let lowerCOWRenameFile = URL(fileURLWithPath: lower).appendingPathComponent(cowRenamePath).path
        let lowerRenameHiddenRollbackSource = URL(fileURLWithPath: lower).appendingPathComponent(renameHiddenRollbackSourcePath).path
        let lowerWriteFile = URL(fileURLWithPath: lower).appendingPathComponent(writePath).path
        let lowerAbsentXattrFile = URL(fileURLWithPath: lower).appendingPathComponent(absentXattrPath).path
        let lowerCopyFailureFile = URL(fileURLWithPath: lower).appendingPathComponent(copyFailurePath).path
        let lowerCopyFailureRenameDestinationDirectory = URL(fileURLWithPath: lower).appendingPathComponent(copyFailureRenameDestinationDirectoryPath).path
        let lowerOversizedXattrFile = URL(fileURLWithPath: lower).appendingPathComponent(oversizedXattrPath).path
        let lowerExistingParentXattrFile = URL(fileURLWithPath: lower).appendingPathComponent(existingParentXattrPath).path
        let lowerReplaceWhiteoutFile = URL(fileURLWithPath: lower).appendingPathComponent(replaceWhiteoutPath).path
        let lowerReplaceHiddenWhiteoutFile = URL(fileURLWithPath: lower).appendingPathComponent(replaceHiddenWhiteoutPath).path
        let lowerRollbackWhiteoutFile = URL(fileURLWithPath: lower).appendingPathComponent(rollbackWhiteoutPath).path
        let lowerSelfRenameFile = URL(fileURLWithPath: lower).appendingPathComponent(selfRenamePath).path
        let lowerRenameOverLowerDirectorySource = URL(fileURLWithPath: lower).appendingPathComponent(renameOverLowerDirectorySourcePath).path
        let lowerRenameOverLowerDirectoryDestinationFile = URL(fileURLWithPath: lower).appendingPathComponent(renameOverLowerDirectoryDestinationPath + "/child.txt").path
        let lowerRenameOverUpperDirectorySource = URL(fileURLWithPath: lower).appendingPathComponent(renameOverUpperDirectorySourcePath).path
        let lowerRenameIntoRemovedDirectorySource = URL(fileURLWithPath: lower).appendingPathComponent(renameIntoRemovedDirectoryPath).path
        let lowerRenameLowerDirectoryFlushRollback = URL(fileURLWithPath: lower).appendingPathComponent(renameLowerDirectoryFlushRollbackPath).path
        let lowerRenameLowerDirectoryFlushRollbackChild = URL(fileURLWithPath: lower).appendingPathComponent(renameLowerDirectoryFlushRollbackPath + "/lower-child.txt").path
        let lowerRenameLowerDirectory = URL(fileURLWithPath: lower).appendingPathComponent(renameLowerDirectoryPath).path
        let lowerRenameLowerDirectoryChild = URL(fileURLWithPath: lower).appendingPathComponent(renameLowerDirectoryPath + "/lower-child.txt").path
        let lowerRenameIntoSelfDirectory = URL(fileURLWithPath: lower).appendingPathComponent(renameIntoSelfDirectoryPath).path
        let lowerRenameIntoSelfChildDirectory = URL(fileURLWithPath: lower).appendingPathComponent(renameIntoSelfDirectoryPath + "/child").path
        let lowerRenameIntoSelfChildFile = URL(fileURLWithPath: lower).appendingPathComponent(renameIntoSelfDirectoryPath + "/child/file.txt").path
        let lowerDanglingLink = URL(fileURLWithPath: lower).appendingPathComponent(danglingLinkPath).path
        let lowerIgnoredDirectory = URL(fileURLWithPath: lower).appendingPathComponent(ignoredDirectoryPath).path
        let lowerIgnoredChild = URL(fileURLWithPath: lower).appendingPathComponent(ignoredDirectoryPath + "/child.txt").path
        let lowerUnsupportedCreateDirectory = URL(fileURLWithPath: lower).appendingPathComponent(unsupportedCreateDirectoryPath).path
        let lowerEmptySymlinkDirectory = URL(fileURLWithPath: lower).appendingPathComponent(emptySymlinkDirectoryPath).path
        let lowerStaleTypeDirectory = URL(fileURLWithPath: lower).appendingPathComponent(staleTypeDirectoryPath).path
        let lowerRemovedDirectory = URL(fileURLWithPath: lower).appendingPathComponent(removedDirectoryPath).path
        let lowerRemovedFile = URL(fileURLWithPath: lower).appendingPathComponent(removedDirectoryPath + "/stale.txt").path
        let lowerNonEmptyDirectory = URL(fileURLWithPath: lower).appendingPathComponent(nonEmptyLowerDirectoryPath).path
        let lowerNonEmptyFile = URL(fileURLWithPath: lower).appendingPathComponent(nonEmptyLowerDirectoryPath + "/child.txt").path
        let lowerXattrDirectory = URL(fileURLWithPath: lower).appendingPathComponent(xattrDirectoryPath).path
        let upperIgnoredDirectory = URL(fileURLWithPath: upper).appendingPathComponent(ignoredDirectoryPath).path
        let upperIgnoredChild = URL(fileURLWithPath: upper).appendingPathComponent(ignoredDirectoryPath + "/child.txt").path
        let upperUnsupportedCreateDirectory = URL(fileURLWithPath: upper).appendingPathComponent(unsupportedCreateDirectoryPath).path
        let upperEmptySymlinkDirectory = URL(fileURLWithPath: upper).appendingPathComponent(emptySymlinkDirectoryPath).path
        let upperStaleTypeDirectory = URL(fileURLWithPath: upper).appendingPathComponent(staleTypeDirectoryPath).path
        let upperStaleCreate = URL(fileURLWithPath: upper).appendingPathComponent(removedDirectoryPath + "/should-not-exist.txt").path
        let upperNonEmptyDirectory = URL(fileURLWithPath: upper).appendingPathComponent(nonEmptyUpperDirectoryPath).path
        let upperNonEmptyFile = URL(fileURLWithPath: upper).appendingPathComponent(nonEmptyUpperDirectoryPath + "/child.txt").path
        let upperNonEmptyLowerWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/.wh.non-empty-lower").path
        let upperXattrDirectory = URL(fileURLWithPath: upper).appendingPathComponent(xattrDirectoryPath).path
        let upperFile = URL(fileURLWithPath: upper).appendingPathComponent(relativePath).path
        let upperDeleteWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.delete-me.txt").path
        let upperRenameDestination = URL(fileURLWithPath: upper).appendingPathComponent(staleRenameDestinationPath).path
        let upperRenameWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-me.txt").path
        let upperNameBoundaryRemoveWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.name-boundary-remove.txt").path
        let upperNameBoundaryRenameDestination = URL(fileURLWithPath: upper).appendingPathComponent(nameBoundaryRenameDestinationPath).path
        let upperNameBoundaryRenameWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.name-boundary-rename.txt").path
        let upperReservedWhiteoutName = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.user-visible.txt").path
        let upperRenameOverLowerFileDestination = URL(fileURLWithPath: upper).appendingPathComponent(renameOverLowerFileDestinationPath).path
        let upperRenameOverLowerFileWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-over-lower-file-source.txt").path
        let upperRenameOverLowerFileDestinationWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-over-lower-file-dest.txt").path
        let upperFailedAttributesFile = URL(fileURLWithPath: upper).appendingPathComponent(failedAttributesPath).path
        let upperCOWDeleteFile = URL(fileURLWithPath: upper).appendingPathComponent(cowDeletePath).path
        let upperCOWDeleteWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.cow-delete.txt").path
        let upperRemoveFlushRollbackFile = URL(fileURLWithPath: upper).appendingPathComponent(removeFlushRollbackPath).path
        let upperRemoveFlushRollbackWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.remove-flush-rollback.txt").path
        let upperCOWRenameDestination = URL(fileURLWithPath: upper).appendingPathComponent(cowRenameDestinationPath).path
        let upperCOWRenameWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.cow-rename.txt").path
        let upperRenameHiddenRollbackSource = URL(fileURLWithPath: upper).appendingPathComponent(renameHiddenRollbackSourcePath).path
        let upperRenameHiddenRollbackSourceWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-hidden-rollback-source.txt").path
        let upperRenameHiddenRollbackDestination = URL(fileURLWithPath: upper).appendingPathComponent(renameHiddenRollbackDestinationPath).path
        let upperRenameHiddenRollbackDestinationWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-hidden-rollback-dest.txt").path
        let upperWriteFile = URL(fileURLWithPath: upper).appendingPathComponent(writePath).path
        let upperAbsentXattrFile = URL(fileURLWithPath: upper).appendingPathComponent(absentXattrPath).path
        let upperCopyFailureFile = URL(fileURLWithPath: upper).appendingPathComponent(copyFailurePath).path
        let upperCopyFailureDirectory = URL(fileURLWithPath: upper).appendingPathComponent("agent/copy-failure").path
        let upperCopyFailureRenameDestination = URL(fileURLWithPath: upper).appendingPathComponent(copyFailureRenameDestinationPath).path
        let upperCopyFailureRenameDestinationDirectory = URL(fileURLWithPath: upper).appendingPathComponent(copyFailureRenameDestinationDirectoryPath).path
        let upperOversizedXattrFile = URL(fileURLWithPath: upper).appendingPathComponent(oversizedXattrPath).path
        let upperOversizedXattrDirectory = URL(fileURLWithPath: upper).appendingPathComponent("agent/xattr-rollback").path
        let upperExistingParentXattrFile = URL(fileURLWithPath: upper).appendingPathComponent(existingParentXattrPath).path
        let upperExistingParentXattrDirectory = URL(fileURLWithPath: upper).appendingPathComponent("agent/existing-upper-parent").path
        let upperReplaceWhiteoutFile = URL(fileURLWithPath: upper).appendingPathComponent(replaceWhiteoutPath).path
        let upperReplaceWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.replace-whiteout.txt").path
        let upperReplaceHiddenWhiteoutFile = URL(fileURLWithPath: upper).appendingPathComponent(replaceHiddenWhiteoutPath).path
        let upperReplaceHiddenWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.replace-hidden-whiteout.txt").path
        let upperRollbackWhiteoutFile = URL(fileURLWithPath: upper).appendingPathComponent(rollbackWhiteoutPath).path
        let upperRollbackWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rollback-whiteout.txt").path
        let upperRollbackHiddenWhiteoutFile = URL(fileURLWithPath: upper).appendingPathComponent(rollbackHiddenWhiteoutPath).path
        let upperRollbackHiddenWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rollback-hidden-whiteout.txt").path
        let upperCreateFlushRollbackHiddenWhiteoutFile = URL(fileURLWithPath: upper).appendingPathComponent(createFlushRollbackHiddenWhiteoutPath).path
        let upperCreateFlushRollbackHiddenWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.create-flush-rollback-hidden-whiteout.txt").path
        let upperSelfRenameFile = URL(fileURLWithPath: upper).appendingPathComponent(selfRenamePath).path
        let upperSelfRenameWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.self-rename.txt").path
        let upperRenameOverLowerDirectorySource = URL(fileURLWithPath: upper).appendingPathComponent(renameOverLowerDirectorySourcePath).path
        let upperRenameOverLowerDirectoryWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-over-lower-dir-source.txt").path
        let upperRenameOverUpperDirectorySource = URL(fileURLWithPath: upper).appendingPathComponent(renameOverUpperDirectorySourcePath).path
        let upperRenameOverUpperDirectoryDestinationFile = URL(fileURLWithPath: upper).appendingPathComponent(renameOverUpperDirectoryDestinationPath + "/child.txt").path
        let upperRenameOverUpperDirectoryWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-over-upper-dir-source.txt").path
        let upperRenameIntoRemovedDirectoryDestination = URL(fileURLWithPath: upper).appendingPathComponent(removedDirectoryPath + "/should-not-move.txt").path
        let upperRenameIntoRemovedDirectoryWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-into-removed.txt").path
        let upperRenameLowerDirectoryFlushRollback = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryFlushRollbackPath).path
        let upperRenameLowerDirectoryFlushRollbackChild = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryFlushRollbackPath + "/upper-child.txt").path
        let upperRenameLowerDirectoryFlushRollbackWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-lower-dir-flush-rollback").path
        let upperRenamedLowerDirectoryFlushRollback = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryFlushRollbackDestinationPath).path
        let upperRenameLowerDirectory = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryPath).path
        let upperRenameLowerDirectoryChild = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryPath + "/upper-child.txt").path
        let upperRenameLowerDirectoryWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-lower-dir").path
        let upperRenamedLowerDirectory = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryDestinationPath).path
        let upperRenamedLowerDirectoryLowerChild = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryDestinationPath + "/lower-child.txt").path
        let upperRenamedLowerDirectoryUpperChild = URL(fileURLWithPath: upper).appendingPathComponent(renameLowerDirectoryDestinationPath + "/upper-child.txt").path
        let upperRenameIntoSelfDirectory = URL(fileURLWithPath: upper).appendingPathComponent(renameIntoSelfDirectoryPath).path
        let upperRenameIntoSelfWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/.wh.rename-into-self").path
        let upperDanglingRemove = URL(fileURLWithPath: upper).appendingPathComponent(upperDanglingRemovePath).path
        let upperDanglingRename = URL(fileURLWithPath: upper).appendingPathComponent(upperDanglingRenamePath).path
        let upperDanglingRenameDestination = URL(fileURLWithPath: upper).appendingPathComponent(upperDanglingRenameDestinationPath).path
        let removedDirectoryWhiteout = URL(fileURLWithPath: upper).appendingPathComponent("agent/.wh.removed").path
        let dirtyFile = URL(fileURLWithPath: work).deletingLastPathComponent().appendingPathComponent("dirty.json").path
        if getuid() != 0 {
            let unreadableDirtyDirectory = URL(fileURLWithPath: upper).appendingPathComponent("agent/dirty-scan-failure").path
            try FileManager.default.createDirectory(atPath: unreadableDirtyDirectory, withIntermediateDirectories: true)
            guard chmod(unreadableDirtyDirectory, 0) == 0 else {
                throw SmokeError("failed to prepare unreadable dirty-index fixture")
            }
            defer {
                chmod(unreadableDirtyDirectory, 0o755)
                try? FileManager.default.removeItem(atPath: unreadableDirtyDirectory)
            }
            do {
                _ = try OSIxDirtyIndex.rebuild(upper: upper)
                throw SmokeError("dirty-index rebuild ignored unreadable upper directory")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
            }
        }
        try Data("delete".utf8).write(to: URL(fileURLWithPath: lowerDeleteFile))
        try Data("rename".utf8).write(to: URL(fileURLWithPath: lowerRenameFile))
        try Data("name boundary remove".utf8).write(to: URL(fileURLWithPath: lowerNameBoundaryRemoveFile))
        try Data("name boundary rename".utf8).write(to: URL(fileURLWithPath: lowerNameBoundaryRenameFile))
        try Data("rename over lower source".utf8).write(to: URL(fileURLWithPath: lowerRenameOverLowerFileSource))
        try Data("rename over lower destination".utf8).write(to: URL(fileURLWithPath: lowerRenameOverLowerFileDestination))
        try Data("failed attrs".utf8).write(to: URL(fileURLWithPath: lowerFailedAttributesFile))
        try Data("cow-delete".utf8).write(to: URL(fileURLWithPath: lowerCOWDeleteFile))
        try Data("remove flush rollback lower".utf8).write(to: URL(fileURLWithPath: lowerRemoveFlushRollbackFile))
        try Data("cow-rename".utf8).write(to: URL(fileURLWithPath: lowerCOWRenameFile))
        try Data("hidden rollback source".utf8).write(to: URL(fileURLWithPath: lowerRenameHiddenRollbackSource))
        try Data("write".utf8).write(to: URL(fileURLWithPath: lowerWriteFile))
        try Data("absent xattr".utf8).write(to: URL(fileURLWithPath: lowerAbsentXattrFile))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerCopyFailureFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("copy failure".utf8).write(to: URL(fileURLWithPath: lowerCopyFailureFile))
        guard chmod(lowerCopyFailureFile, 0) == 0 else {
            throw SmokeError("failed to prepare unreadable copy-up fixture")
        }
        try FileManager.default.createDirectory(atPath: lowerCopyFailureRenameDestinationDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerOversizedXattrFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("oversized xattr".utf8).write(to: URL(fileURLWithPath: lowerOversizedXattrFile))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerExistingParentXattrFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("existing parent xattr".utf8).write(to: URL(fileURLWithPath: lowerExistingParentXattrFile))
        try Data("hidden lower".utf8).write(to: URL(fileURLWithPath: lowerReplaceWhiteoutFile))
        try Data("hidden lower poisoned".utf8).write(to: URL(fileURLWithPath: lowerReplaceHiddenWhiteoutFile))
        try Data("rollback lower".utf8).write(to: URL(fileURLWithPath: lowerRollbackWhiteoutFile))
        try Data("self".utf8).write(to: URL(fileURLWithPath: lowerSelfRenameFile))
        try Data("lower dir source".utf8).write(to: URL(fileURLWithPath: lowerRenameOverLowerDirectorySource))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerRenameOverLowerDirectoryDestinationFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("lower dir child".utf8).write(to: URL(fileURLWithPath: lowerRenameOverLowerDirectoryDestinationFile))
        try Data("upper dir source".utf8).write(to: URL(fileURLWithPath: lowerRenameOverUpperDirectorySource))
        try Data("rename into removed".utf8).write(to: URL(fileURLWithPath: lowerRenameIntoRemovedDirectorySource))
        try FileManager.default.createDirectory(atPath: lowerRenameLowerDirectoryFlushRollback, withIntermediateDirectories: true)
        try Data("lower rollback child".utf8).write(to: URL(fileURLWithPath: lowerRenameLowerDirectoryFlushRollbackChild))
        try FileManager.default.createDirectory(atPath: lowerRenameLowerDirectory, withIntermediateDirectories: true)
        try Data("lower rename child".utf8).write(to: URL(fileURLWithPath: lowerRenameLowerDirectoryChild))
        try FileManager.default.createDirectory(atPath: upperRenameLowerDirectoryFlushRollback, withIntermediateDirectories: true)
        try Data("upper rollback child".utf8).write(to: URL(fileURLWithPath: upperRenameLowerDirectoryFlushRollbackChild))
        try FileManager.default.createDirectory(atPath: upperRenameLowerDirectory, withIntermediateDirectories: true)
        try Data("upper rename child".utf8).write(to: URL(fileURLWithPath: upperRenameLowerDirectoryChild))
        try FileManager.default.createDirectory(atPath: lowerRenameIntoSelfChildDirectory, withIntermediateDirectories: true)
        try Data("self child".utf8).write(to: URL(fileURLWithPath: lowerRenameIntoSelfChildFile))
        try FileManager.default.createSymbolicLink(atPath: lowerDanglingLink, withDestinationPath: "missing-target")
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: upperDanglingRemove).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(atPath: upperDanglingRemove, withDestinationPath: "missing-upper-remove-target")
        try FileManager.default.createSymbolicLink(atPath: upperDanglingRename, withDestinationPath: "missing-upper-rename-target")
        try FileManager.default.createDirectory(atPath: lowerIgnoredDirectory, withIntermediateDirectories: true)
        try Data("ignored child".utf8).write(to: URL(fileURLWithPath: lowerIgnoredChild))
        try FileManager.default.createDirectory(atPath: lowerUnsupportedCreateDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: lowerEmptySymlinkDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: lowerStaleTypeDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerRemovedFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("stale".utf8).write(to: URL(fileURLWithPath: lowerRemovedFile))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: lowerNonEmptyFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("lower child".utf8).write(to: URL(fileURLWithPath: lowerNonEmptyFile))
        try FileManager.default.createDirectory(atPath: lowerXattrDirectory, withIntermediateDirectories: true)
        try setRawXattr(path: lowerXattrDirectory, name: "osix.policy", value: Data("lower".utf8), options: 0)
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: upperNonEmptyFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("upper child".utf8).write(to: URL(fileURLWithPath: upperNonEmptyFile))
        try Data("remove flush rollback upper".utf8).write(to: URL(fileURLWithPath: upperRemoveFlushRollbackFile))
        try FileManager.default.createDirectory(atPath: upperExistingParentXattrDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: upperRenameOverUpperDirectoryDestinationFile).deletingLastPathComponent().path, withIntermediateDirectories: true)
        try Data("upper dir child".utf8).write(to: URL(fileURLWithPath: upperRenameOverUpperDirectoryDestinationFile))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: upperReplaceWhiteout).deletingLastPathComponent().path, withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: upperReplaceWhiteout, contents: Data())
        FileManager.default.createFile(atPath: upperReplaceHiddenWhiteout, contents: Data())
        try Data("poisoned hidden upper".utf8).write(to: URL(fileURLWithPath: upperReplaceHiddenWhiteoutFile))
        FileManager.default.createFile(atPath: upperRollbackWhiteout, contents: Data())
        FileManager.default.createFile(atPath: upperRollbackHiddenWhiteout, contents: Data())
        try Data("hidden rollback".utf8).write(to: URL(fileURLWithPath: upperRollbackHiddenWhiteoutFile))
        FileManager.default.createFile(atPath: upperCreateFlushRollbackHiddenWhiteout, contents: Data())
        try Data("hidden create flush rollback".utf8).write(to: URL(fileURLWithPath: upperCreateFlushRollbackHiddenWhiteoutFile))
        FileManager.default.createFile(atPath: upperRenameHiddenRollbackDestinationWhiteout, contents: Data())
        try Data("hidden rename rollback".utf8).write(to: URL(fileURLWithPath: upperRenameHiddenRollbackDestination))
        try FileManager.default.createDirectory(atPath: URL(fileURLWithPath: removedDirectoryWhiteout).deletingLastPathComponent().path, withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: removedDirectoryWhiteout, contents: Data())
        try validateMountOptions(lower: lower, upper: upper, work: work)

        let volume = OSIxVolume(
            volumeID: FSVolume.Identifier(uuid: UUID()),
            volumeName: FSFileName(string: "OSIxSmoke"),
            mountOptions: OSIxMountOptions(
                bundle: nil,
                workspace: nil,
                sourceRef: nil,
                sourceDigest: nil,
                lower: lower,
                upper: upper,
                work: work,
                mode: "overlay"
            )
        )
        let item = OSIxItem(relativePath: relativePath, physicalPath: lowerFile, type: .file, source: .lower)
        let request = FSItem.SetAttributesRequest()
        request.size = 2
        request.mode = 0o600

        var replyAttributes: FSItem.Attributes?
        var replyError: (any Error)?
        volume.setAttributes(request, on: item) { attributes, error in
            replyAttributes = attributes
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let replyAttributes else {
            throw SmokeError("setAttributes returned no attributes")
        }
        guard request.wasAttributeConsumed(.size), request.wasAttributeConsumed(.mode) else {
            throw SmokeError("setAttributes did not report consumed size and mode attributes")
        }

        let lowerData = try Data(contentsOf: URL(fileURLWithPath: lowerFile))
        let upperData = try Data(contentsOf: URL(fileURLWithPath: upperFile))
        guard String(data: lowerData, encoding: .utf8) == "lower" else {
            throw SmokeError("lower file was modified")
        }
        guard String(data: upperData, encoding: .utf8) == "lo" else {
            throw SmokeError("upper file does not contain truncated copy-on-write data")
        }
        let upperAttributes = try FileManager.default.attributesOfItem(atPath: upperFile)
        let upperMode = (upperAttributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard upperMode == 0o600 else {
            throw SmokeError(String(format: "upper mode is %04o, want 0600", upperMode))
        }
        guard replyAttributes.size == 2, replyAttributes.mode == 0o600 else {
            throw SmokeError("reply attributes do not reflect upper metadata")
        }
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        let flushFailureAttributes = FSItem.SetAttributesRequest()
        flushFailureAttributes.size = 1
        flushFailureAttributes.mode = 0o644
        do {
            _ = try setAttributes(volume: volume, request: flushFailureAttributes, item: item)
            throw SmokeError("setAttributes succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        let restoredUpperData = try Data(contentsOf: URL(fileURLWithPath: upperFile))
        let restoredUpperAttributes = try FileManager.default.attributesOfItem(atPath: upperFile)
        let restoredUpperMode = (restoredUpperAttributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard String(data: restoredUpperData, encoding: .utf8) == "lo", restoredUpperMode == 0o600 else {
            throw SmokeError("failed setAttributes flush rollback did not restore upper metadata/content")
        }
        if getuid() != 0 {
            defer {
                chmod(lowerCopyFailureFile, 0o644)
            }
            let failedAttributesItem = OSIxItem(relativePath: failedAttributesPath, physicalPath: lowerFailedAttributesFile, type: .file, source: .lower)
            let failedAttributesRequest = FSItem.SetAttributesRequest()
            failedAttributesRequest.uid = 0
            do {
                _ = try setAttributes(volume: volume, request: failedAttributesRequest, item: failedAttributesItem)
                throw SmokeError("setAttributes unexpectedly changed lower file owner")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EPERM) {
            }
            guard !itemExistsNoFollow(upperFailedAttributesFile),
                  (try? String(contentsOfFile: lowerFailedAttributesFile, encoding: .utf8)) == "failed attrs" else {
                throw SmokeError("failed setAttributes left upper copy or changed lower file")
            }
            let copyFailureItem = OSIxItem(relativePath: copyFailurePath, physicalPath: lowerCopyFailureFile, type: .file, source: .lower)
            let copyFailureRequest = FSItem.SetAttributesRequest()
            copyFailureRequest.mode = 0o600
            do {
                _ = try setAttributes(volume: volume, request: copyFailureRequest, item: copyFailureItem)
                throw SmokeError("setAttributes unexpectedly copied unreadable lower file")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
            }
            guard !itemExistsNoFollow(upperCopyFailureFile),
                  !FileManager.default.fileExists(atPath: upperCopyFailureDirectory) else {
                throw SmokeError("failed copy-up left upper copy or parent state")
            }
            do {
                try renameItem(
                    volume: volume,
                    item: copyFailureItem,
                    sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/copy-failure"),
                    sourceName: FSFileName(string: "unreadable.txt"),
                    destinationName: FSFileName(string: "renamed.txt"),
                    destinationDirectory: workspaceItem(lower: lower, relativePath: copyFailureRenameDestinationDirectoryPath)
                )
                throw SmokeError("renameItem unexpectedly copied unreadable lower file")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
            }
            guard !itemExistsNoFollow(upperCopyFailureFile),
                  !itemExistsNoFollow(upperCopyFailureRenameDestination),
                  !FileManager.default.fileExists(atPath: upperCopyFailureDirectory),
                  !FileManager.default.fileExists(atPath: upperCopyFailureRenameDestinationDirectory) else {
                throw SmokeError("failed rename copy-up left upper source or destination parent state")
            }
        }

        let ignoredDirectory = OSIxItem(relativePath: ignoredDirectoryPath, physicalPath: lowerIgnoredDirectory, type: .directory, source: .lower)
        let ignoredSize = FSItem.SetAttributesRequest()
        ignoredSize.size = 99
        _ = try setAttributes(volume: volume, request: ignoredSize, item: ignoredDirectory)
        guard !ignoredSize.wasAttributeConsumed(.size) else {
            throw SmokeError("directory size request was incorrectly consumed")
        }
        guard !FileManager.default.fileExists(atPath: upperIgnoredDirectory) else {
            throw SmokeError("ignored directory size request copied lower directory into upper")
        }
        do {
            try createItem(volume: volume, name: FSFileName(string: "unsupported-link"), type: .symlink, directory: workspaceItem(lower: lower, relativePath: unsupportedCreateDirectoryPath), attributes: FSItem.SetAttributesRequest())
            throw SmokeError("createItem accepted unsupported symlink item type")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOTSUP) {
        }
        guard !FileManager.default.fileExists(atPath: upperUnsupportedCreateDirectory) else {
            throw SmokeError("unsupported createItem left upperdir parent state")
        }
        do {
            try createSymbolicLink(volume: volume, name: FSFileName(string: "empty-link"), directory: workspaceItem(lower: lower, relativePath: emptySymlinkDirectoryPath), contents: FSFileName(string: ""), attributes: FSItem.SetAttributesRequest())
            throw SmokeError("createSymbolicLink accepted empty target")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        guard !FileManager.default.fileExists(atPath: upperEmptySymlinkDirectory) else {
            throw SmokeError("empty-target createSymbolicLink left upperdir parent state")
        }
        let directoryMode = FSItem.SetAttributesRequest()
        directoryMode.mode = 0o700
        _ = try setAttributes(volume: volume, request: directoryMode, item: ignoredDirectory)
        let upperIgnoredAttributes = try FileManager.default.attributesOfItem(atPath: upperIgnoredDirectory)
        let upperIgnoredMode = (upperIgnoredAttributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard directoryMode.wasAttributeConsumed(.mode),
              upperIgnoredMode == 0o700,
              !FileManager.default.fileExists(atPath: upperIgnoredChild) else {
            throw SmokeError("directory metadata copy-up copied lower children into upper")
        }
        let staleTypedFileForDirectory = OSIxItem(relativePath: staleTypeDirectoryPath, physicalPath: lowerStaleTypeDirectory, type: .file, source: .lower)
        let staleTypeSize = FSItem.SetAttributesRequest()
        staleTypeSize.size = 99
        let staleTypeAttributes = try setAttributes(volume: volume, request: staleTypeSize, item: staleTypedFileForDirectory)
        guard staleTypeAttributes.type == .directory,
              !staleTypeSize.wasAttributeConsumed(.size),
              !FileManager.default.fileExists(atPath: upperStaleTypeDirectory) else {
            throw SmokeError("stale file-typed directory setAttributes copied up or consumed size")
        }

        let staleRemovedChild = OSIxItem(relativePath: removedDirectoryPath + "/stale.txt", physicalPath: lowerRemovedFile, type: .file, source: .lower)
        do {
            _ = try getAttributes(volume: volume, item: staleRemovedChild)
            throw SmokeError("stale lower child under whiteouted directory remained visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let staleRemovedDirectory = OSIxItem(relativePath: removedDirectoryPath, physicalPath: lowerRemovedDirectory, type: .directory, source: .lower)
        do {
            try createItem(volume: volume, name: FSFileName(string: "should-not-exist.txt"), type: .file, directory: staleRemovedDirectory, attributes: FSItem.SetAttributesRequest())
            throw SmokeError("createItem succeeded in a stale whiteouted lower directory")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        guard !FileManager.default.fileExists(atPath: upperStaleCreate) else {
            throw SmokeError("createItem wrote into a stale whiteouted lower directory")
        }
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            let flushFailureRequest = FSItem.SetAttributesRequest()
            flushFailureRequest.size = 3
            try createItem(volume: volume, name: FSFileName(string: "create-flush-rollback-hidden-whiteout.txt"), type: .file, directory: workspaceItem(lower: lower, relativePath: "agent/workspace"), attributes: flushFailureRequest)
            throw SmokeError("createItem over hidden whiteout succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard (try? String(contentsOfFile: upperCreateFlushRollbackHiddenWhiteoutFile, encoding: .utf8)) == "hidden create flush rollback",
              FileManager.default.fileExists(atPath: upperCreateFlushRollbackHiddenWhiteout) else {
            throw SmokeError("failed createItem flush rollback did not restore hidden upper state")
        }
        do {
            _ = try lookupItem(volume: volume, name: FSFileName(string: "create-flush-rollback-hidden-whiteout.txt"), directory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
            throw SmokeError("failed createItem flush rollback exposed hidden upper item")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        if getuid() != 0 {
            let failingCreateRequest = FSItem.SetAttributesRequest()
            failingCreateRequest.uid = 0
            do {
                try createItem(volume: volume, name: FSFileName(string: "rollback-whiteout.txt"), type: .file, directory: workspaceItem(lower: lower, relativePath: "agent/workspace"), attributes: failingCreateRequest)
                throw SmokeError("createItem over whiteout unexpectedly changed file owner")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EPERM) {
            }
            guard !itemExistsNoFollow(upperRollbackWhiteoutFile),
                  FileManager.default.fileExists(atPath: upperRollbackWhiteout) else {
                throw SmokeError("failed createItem over whiteout left hidden upper state")
            }
            do {
                try createItem(volume: volume, name: FSFileName(string: "rollback-hidden-whiteout.txt"), type: .file, directory: workspaceItem(lower: lower, relativePath: "agent/workspace"), attributes: failingCreateRequest)
                throw SmokeError("createItem over hidden whiteout unexpectedly changed file owner")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EPERM) {
            }
            guard (try? String(contentsOfFile: upperRollbackHiddenWhiteoutFile, encoding: .utf8)) == "hidden rollback",
                  FileManager.default.fileExists(atPath: upperRollbackHiddenWhiteout) else {
                throw SmokeError("failed createItem over hidden whiteout did not restore hidden upper state")
            }
            do {
                _ = try lookupItem(volume: volume, name: FSFileName(string: "rollback-whiteout.txt"), directory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
                throw SmokeError("failed createItem over whiteout exposed lower item")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
            }
            do {
                _ = try lookupItem(volume: volume, name: FSFileName(string: "rollback-hidden-whiteout.txt"), directory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
                throw SmokeError("failed createItem over hidden whiteout exposed hidden upper item")
            } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
            }
        }
        let renameIntoRemovedDirectory = OSIxItem(relativePath: renameIntoRemovedDirectoryPath, physicalPath: lowerRenameIntoRemovedDirectorySource, type: .file, source: .lower)
        do {
            try renameItem(volume: volume, item: renameIntoRemovedDirectory, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-into-removed.txt"), destinationName: FSFileName(string: "should-not-move.txt"), destinationDirectory: staleRemovedDirectory)
            throw SmokeError("renameItem succeeded into a stale whiteouted lower directory")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        guard (try? String(contentsOfFile: lowerRenameIntoRemovedDirectorySource, encoding: .utf8)) == "rename into removed",
              !itemExistsNoFollow(upperRenameIntoRemovedDirectoryDestination),
              !FileManager.default.fileExists(atPath: upperRenameIntoRemovedDirectoryWhiteout) else {
            throw SmokeError("renameItem into stale whiteouted directory mutated filesystem state")
        }
        let replaceRequest = FSItem.SetAttributesRequest()
        replaceRequest.size = 1
        try createItem(volume: volume, name: FSFileName(string: "replace-whiteout.txt"), type: .file, directory: workspaceItem(lower: lower, relativePath: "agent/workspace"), attributes: replaceRequest)
        guard !FileManager.default.fileExists(atPath: upperReplaceWhiteout), FileManager.default.fileExists(atPath: upperReplaceWhiteoutFile) else {
            throw SmokeError("createItem over whiteout did not replace whiteout with upper file")
        }
        guard (try FileManager.default.attributesOfItem(atPath: upperReplaceWhiteoutFile)[.size] as? NSNumber)?.intValue == 1 else {
            throw SmokeError("createItem over whiteout did not apply requested file size")
        }
        let replaceItem = try lookupItem(volume: volume, name: FSFileName(string: "replace-whiteout.txt"), directory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
        guard try getAttributes(volume: volume, item: replaceItem).size == 1 else {
            throw SmokeError("createItem over whiteout exposed lower file instead of upper replacement")
        }
        let replaceHiddenRequest = FSItem.SetAttributesRequest()
        replaceHiddenRequest.size = 2
        try createItem(volume: volume, name: FSFileName(string: "replace-hidden-whiteout.txt"), type: .file, directory: workspaceItem(lower: lower, relativePath: "agent/workspace"), attributes: replaceHiddenRequest)
        guard !FileManager.default.fileExists(atPath: upperReplaceHiddenWhiteout),
              (try? Data(contentsOf: URL(fileURLWithPath: upperReplaceHiddenWhiteoutFile))) == Data(repeating: 0, count: 2) else {
            throw SmokeError("createItem over whiteout did not replace hidden upper state")
        }
        let selfRename = OSIxItem(relativePath: selfRenamePath, physicalPath: lowerSelfRenameFile, type: .file, source: .lower)
        try renameItem(volume: volume, item: selfRename, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "self-rename.txt"), destinationName: FSFileName(string: "self-rename.txt"), destinationDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
        guard (try? String(contentsOfFile: lowerSelfRenameFile, encoding: .utf8)) == "self" else {
            throw SmokeError("rename-to-self changed lower source")
        }
        guard !itemExistsNoFollow(upperSelfRenameFile), !FileManager.default.fileExists(atPath: upperSelfRenameWhiteout) else {
            throw SmokeError("rename-to-self mutated upperdir")
        }
        let renameOverLowerDir = OSIxItem(relativePath: renameOverLowerDirectorySourcePath, physicalPath: lowerRenameOverLowerDirectorySource, type: .file, source: .lower)
        do {
            try renameItem(volume: volume, item: renameOverLowerDir, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-over-lower-dir-source.txt"), destinationName: FSFileName(string: "rename-over-lower-dir-dest"), destinationDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
            throw SmokeError("rename file over lower directory succeeded")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EISDIR) {
        }
        guard (try? String(contentsOfFile: lowerRenameOverLowerDirectorySource, encoding: .utf8)) == "lower dir source",
              FileManager.default.fileExists(atPath: lowerRenameOverLowerDirectoryDestinationFile),
              !itemExistsNoFollow(upperRenameOverLowerDirectorySource),
              !FileManager.default.fileExists(atPath: upperRenameOverLowerDirectoryWhiteout) else {
            throw SmokeError("rename file over lower directory mutated filesystem state")
        }
        let renameOverUpperDir = OSIxItem(relativePath: renameOverUpperDirectorySourcePath, physicalPath: lowerRenameOverUpperDirectorySource, type: .file, source: .lower)
        do {
            try renameItem(volume: volume, item: renameOverUpperDir, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-over-upper-dir-source.txt"), destinationName: FSFileName(string: "rename-over-upper-dir-dest"), destinationDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
            throw SmokeError("rename file over upper directory succeeded")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EISDIR) {
        }
        guard (try? String(contentsOfFile: lowerRenameOverUpperDirectorySource, encoding: .utf8)) == "upper dir source",
              FileManager.default.fileExists(atPath: upperRenameOverUpperDirectoryDestinationFile),
              !itemExistsNoFollow(upperRenameOverUpperDirectorySource),
              !FileManager.default.fileExists(atPath: upperRenameOverUpperDirectoryWhiteout) else {
            throw SmokeError("rename file over upper directory mutated filesystem state")
        }
        let renameIntoSelfDirectory = OSIxItem(relativePath: renameIntoSelfDirectoryPath, physicalPath: lowerRenameIntoSelfDirectory, type: .directory, source: .lower)
        let renameIntoSelfChildDirectory = OSIxItem(relativePath: renameIntoSelfDirectoryPath + "/child", physicalPath: lowerRenameIntoSelfChildDirectory, type: .directory, source: .lower)
        do {
            try renameItem(volume: volume, item: renameIntoSelfDirectory, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-into-self"), destinationName: FSFileName(string: "nested"), destinationDirectory: renameIntoSelfChildDirectory)
            throw SmokeError("rename directory into its own descendant succeeded")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        guard FileManager.default.fileExists(atPath: lowerRenameIntoSelfChildFile),
              !itemExistsNoFollow(upperRenameIntoSelfDirectory),
              !FileManager.default.fileExists(atPath: upperRenameIntoSelfWhiteout) else {
            throw SmokeError("rename directory into own descendant mutated filesystem state")
        }
        let renameLowerDirectoryFlushRollback = OSIxItem(relativePath: renameLowerDirectoryFlushRollbackPath, physicalPath: lowerRenameLowerDirectoryFlushRollback, type: .directory, source: .lower)
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            try renameItem(volume: volume, item: renameLowerDirectoryFlushRollback, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-lower-dir-flush-rollback"), destinationName: FSFileName(string: "renamed-lower-dir-flush-rollback"), destinationDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
            throw SmokeError("lower-covering directory rename succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard (try? String(contentsOfFile: lowerRenameLowerDirectoryFlushRollbackChild, encoding: .utf8)) == "lower rollback child",
              (try? String(contentsOfFile: upperRenameLowerDirectoryFlushRollbackChild, encoding: .utf8)) == "upper rollback child",
              !FileManager.default.fileExists(atPath: upperRenameLowerDirectoryFlushRollbackWhiteout),
              !itemExistsNoFollow(upperRenamedLowerDirectoryFlushRollback) else {
            throw SmokeError("failed lower-covering directory rename did not restore source state")
        }
        _ = try lookupItem(volume: volume, name: FSFileName(string: "rename-lower-dir-flush-rollback"), directory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
        let renameLowerDirectory = OSIxItem(relativePath: renameLowerDirectoryPath, physicalPath: lowerRenameLowerDirectory, type: .directory, source: .lower)
        try renameItem(volume: volume, item: renameLowerDirectory, sourceDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"), sourceName: FSFileName(string: "rename-lower-dir"), destinationName: FSFileName(string: "renamed-lower-dir"), destinationDirectory: workspaceItem(lower: lower, relativePath: "agent/workspace"))
        guard !itemExistsNoFollow(upperRenameLowerDirectory),
              FileManager.default.fileExists(atPath: upperRenameLowerDirectoryWhiteout),
              FileManager.default.fileExists(atPath: upperRenamedLowerDirectory),
              (try? String(contentsOfFile: upperRenamedLowerDirectoryLowerChild, encoding: .utf8)) == "lower rename child",
              (try? String(contentsOfFile: upperRenamedLowerDirectoryUpperChild, encoding: .utf8)) == "upper rename child" else {
            throw SmokeError("lower-covering directory rename did not preserve merged children and clean source")
        }
        do {
            _ = try getAttributes(volume: volume, item: renameLowerDirectory)
            throw SmokeError("lower-covering directory rename left source visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let nonEmptyLowerDirectory = OSIxItem(relativePath: nonEmptyLowerDirectoryPath, physicalPath: lowerNonEmptyDirectory, type: .directory, source: .lower)
        do {
            try removeItem(volume: volume, item: nonEmptyLowerDirectory, name: FSFileName(string: "non-empty-lower"), directory: OSIxItem(
                relativePath: "agent",
                physicalPath: URL(fileURLWithPath: lower).appendingPathComponent("agent").path,
                type: .directory,
                source: .lower
            ))
            throw SmokeError("removeItem removed non-empty lower directory")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOTEMPTY) {
        }
        guard FileManager.default.fileExists(atPath: lowerNonEmptyFile), !FileManager.default.fileExists(atPath: upperNonEmptyLowerWhiteout) else {
            throw SmokeError("non-empty lower directory removal modified filesystem state")
        }
        let nonEmptyUpperDirectory = OSIxItem(relativePath: nonEmptyUpperDirectoryPath, physicalPath: upperNonEmptyDirectory, type: .directory, source: .upper)
        do {
            try removeItem(volume: volume, item: nonEmptyUpperDirectory, name: FSFileName(string: "non-empty-upper"), directory: OSIxItem(
                relativePath: "agent",
                physicalPath: URL(fileURLWithPath: lower).appendingPathComponent("agent").path,
                type: .directory,
                source: .lower
            ))
            throw SmokeError("removeItem removed non-empty upper directory")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOTEMPTY) {
        }
        guard FileManager.default.fileExists(atPath: upperNonEmptyFile) else {
            throw SmokeError("non-empty upper directory removal deleted child")
        }

        let workspace = OSIxItem(
            relativePath: "agent/workspace",
            physicalPath: URL(fileURLWithPath: lower).appendingPathComponent("agent/workspace").path,
            type: .directory,
            source: .lower
        )
        do {
            _ = try lookupItem(volume: volume, name: FSFileName(string: "file.txt/.."), directory: workspace)
            throw SmokeError("lookupItem accepted slash-containing name")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        do {
            _ = try lookupItem(volume: volume, name: FSFileName(string: ".wh.user-visible.txt"), directory: workspace)
            throw SmokeError("lookupItem accepted reserved whiteout name")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        do {
            try createItem(volume: volume, name: FSFileName(string: ".wh.user-visible.txt"), type: .file, directory: workspace, attributes: FSItem.SetAttributesRequest())
            throw SmokeError("createItem accepted reserved whiteout name")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        guard !FileManager.default.fileExists(atPath: upperReservedWhiteoutName) else {
            throw SmokeError("reserved whiteout-name create wrote internal upper state")
        }
        let mismatchedRemoveHandle = OSIxItem(relativePath: staleDeletePath, physicalPath: lowerDeleteFile, type: .file, source: .lower)
        try removeItem(volume: volume, item: mismatchedRemoveHandle, name: FSFileName(string: "name-boundary-remove.txt"), directory: workspace)
        guard FileManager.default.fileExists(atPath: upperNameBoundaryRemoveWhiteout),
              !FileManager.default.fileExists(atPath: upperDeleteWhiteout),
              (try? String(contentsOfFile: lowerDeleteFile, encoding: .utf8)) == "delete" else {
            throw SmokeError("removeItem did not use parent/name as the authoritative removal path")
        }
        do {
            _ = try lookupItem(volume: volume, name: FSFileName(string: "name-boundary-remove.txt"), directory: workspace)
            throw SmokeError("name-boundary remove left removed path visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let mismatchedRenameHandle = OSIxItem(relativePath: staleRenamePath, physicalPath: lowerRenameFile, type: .file, source: .lower)
        try renameItem(volume: volume, item: mismatchedRenameHandle, sourceDirectory: workspace, sourceName: FSFileName(string: "name-boundary-rename.txt"), destinationName: FSFileName(string: "name-boundary-renamed.txt"), destinationDirectory: workspace)
        guard FileManager.default.fileExists(atPath: upperNameBoundaryRenameWhiteout),
              (try? String(contentsOfFile: upperNameBoundaryRenameDestination, encoding: .utf8)) == "name boundary rename",
              !FileManager.default.fileExists(atPath: upperRenameWhiteout),
              (try? String(contentsOfFile: lowerRenameFile, encoding: .utf8)) == "rename" else {
            throw SmokeError("renameItem did not use source parent/name as the authoritative source path")
        }
        let renameOverLowerFile = OSIxItem(relativePath: renameOverLowerFileSourcePath, physicalPath: lowerRenameOverLowerFileSource, type: .file, source: .lower)
        try renameItem(volume: volume, item: renameOverLowerFile, sourceDirectory: workspace, sourceName: FSFileName(string: "rename-over-lower-file-source.txt"), destinationName: FSFileName(string: "rename-over-lower-file-dest.txt"), destinationDirectory: workspace)
        guard FileManager.default.fileExists(atPath: upperRenameOverLowerFileWhiteout),
              (try? String(contentsOfFile: upperRenameOverLowerFileDestination, encoding: .utf8)) == "rename over lower source",
              (try? String(contentsOfFile: lowerRenameOverLowerFileDestination, encoding: .utf8)) == "rename over lower destination",
              !FileManager.default.fileExists(atPath: upperRenameOverLowerFileDestinationWhiteout) else {
            throw SmokeError("rename over lower file did not cover destination with upper source while preserving lower destination")
        }
        do {
            _ = try getAttributes(volume: volume, item: renameOverLowerFile)
            throw SmokeError("rename over lower file left source visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }

        let staleUpperDelete = OSIxItem(relativePath: staleDeletePath, physicalPath: URL(fileURLWithPath: upper).appendingPathComponent(staleDeletePath).path, type: .file, source: .upper)
        try removeItem(volume: volume, item: staleUpperDelete, name: FSFileName(string: "delete-me.txt"), directory: workspace)
        guard FileManager.default.fileExists(atPath: upperDeleteWhiteout) else {
            throw SmokeError("stale upper remove did not whiteout current lower item")
        }
        do {
            _ = try getAttributes(volume: volume, item: staleUpperDelete)
            throw SmokeError("stale upper remove left lower item visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }

        let danglingItem = try lookupItem(volume: volume, name: FSFileName(string: "dangling-link"), directory: workspace)
        guard let danglingOSIxItem = danglingItem as? OSIxItem, danglingOSIxItem.type == .symlink else {
            throw SmokeError("lookupItem did not return dangling symlink")
        }
        let danglingAttributes = try getAttributes(volume: volume, item: danglingItem)
        guard danglingAttributes.type == .symlink, danglingAttributes.size == UInt64("missing-target".utf8.count) else {
            throw SmokeError("dangling symlink attributes did not use lstat metadata")
        }
        guard try readSymbolicLink(volume: volume, item: danglingItem) == "missing-target" else {
            throw SmokeError("readSymbolicLink did not preserve dangling symlink target")
        }
        let symlinkXattrName = FSFileName(string: "osix.symlink")
        try setXattr(volume: volume, name: symlinkXattrName, value: Data("link".utf8), item: danglingItem, policy: .alwaysSet)
        guard (try? FileManager.default.destinationOfSymbolicLink(atPath: URL(fileURLWithPath: upper).appendingPathComponent(danglingLinkPath).path)) == "missing-target" else {
            throw SmokeError("setting xattr on dangling symlink did not copy link into upper")
        }
        guard try getXattr(volume: volume, name: symlinkXattrName, item: danglingItem) == Data("link".utf8) else {
            throw SmokeError("getXattr did not read dangling symlink xattr with nofollow")
        }
        guard try listXattrs(volume: volume, item: danglingItem).contains("osix.symlink") else {
            throw SmokeError("listXattrs did not include dangling symlink xattr")
        }
        try setXattr(volume: volume, name: symlinkXattrName, value: nil, item: danglingItem, policy: .delete)
        guard !((try? listXattrs(volume: volume, item: danglingItem).contains("osix.symlink")) ?? true) else {
            throw SmokeError("setXattr delete did not remove dangling symlink xattr")
        }
        guard getxattr(lowerDanglingLink, "osix.symlink", nil, 0, 0, XATTR_NOFOLLOW) < 0, errno == ENOATTR else {
            throw SmokeError("lower dangling symlink gained xattr data")
        }
        let xattrDirectory = OSIxItem(relativePath: xattrDirectoryPath, physicalPath: lowerXattrDirectory, type: .directory, source: .lower)
        let xattrDirectoryName = FSFileName(string: "osix.policy")
        do {
            try setXattr(volume: volume, name: xattrDirectoryName, value: Data("created".utf8), item: xattrDirectory, policy: .mustCreate)
            throw SmokeError("mustCreate xattr succeeded over visible lower directory xattr")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EEXIST) {
        }
        guard !FileManager.default.fileExists(atPath: upperXattrDirectory) else {
            throw SmokeError("failed mustCreate xattr copied lower directory into upper")
        }
        try setXattr(volume: volume, name: xattrDirectoryName, value: Data("replaced".utf8), item: xattrDirectory, policy: .mustReplace)
        guard try getRawXattr(path: upperXattrDirectory, name: "osix.policy", options: 0) == Data("replaced".utf8) else {
            throw SmokeError("mustReplace xattr did not write replacement onto upper directory")
        }
        try setXattr(volume: volume, name: xattrDirectoryName, value: nil, item: xattrDirectory, policy: .delete)
        do {
            _ = try getXattr(volume: volume, name: xattrDirectoryName, item: xattrDirectory)
            throw SmokeError("delete xattr left upper directory xattr visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOATTR) {
        }
        let absentXattrItem = OSIxItem(relativePath: absentXattrPath, physicalPath: lowerAbsentXattrFile, type: .file, source: .lower)
        do {
            try setXattr(volume: volume, name: FSFileName(string: "osix.absent"), value: nil, item: absentXattrItem, policy: .delete)
            throw SmokeError("setXattr delete succeeded for absent lower xattr")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOATTR) {
        }
        guard !itemExistsNoFollow(upperAbsentXattrFile) else {
            throw SmokeError("failed absent-xattr delete copied lower file into upper")
        }
        let oversizedXattrItem = OSIxItem(relativePath: oversizedXattrPath, physicalPath: lowerOversizedXattrFile, type: .file, source: .lower)
        do {
            try setXattr(volume: volume, name: FSFileName(string: String(repeating: "n", count: 128)), value: Data("bad".utf8), item: oversizedXattrItem, policy: .alwaysSet)
            throw SmokeError("setXattr accepted oversized name")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain {
        }
        guard !itemExistsNoFollow(upperOversizedXattrFile),
              !FileManager.default.fileExists(atPath: upperOversizedXattrDirectory) else {
            throw SmokeError("failed oversized xattr left upper copy or parent state")
        }
        let existingParentXattrItem = OSIxItem(relativePath: existingParentXattrPath, physicalPath: lowerExistingParentXattrFile, type: .file, source: .lower)
        do {
            try setXattr(volume: volume, name: FSFileName(string: String(repeating: "p", count: 128)), value: Data("bad".utf8), item: existingParentXattrItem, policy: .alwaysSet)
            throw SmokeError("setXattr accepted oversized name with existing upper parent")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain {
        }
        guard !itemExistsNoFollow(upperExistingParentXattrFile),
              FileManager.default.fileExists(atPath: upperExistingParentXattrDirectory) else {
            throw SmokeError("failed oversized xattr removed preexisting upper parent or left upper copy")
        }
        let upperDanglingRemoveItem = OSIxItem(relativePath: upperDanglingRemovePath, physicalPath: upperDanglingRemove, type: .symlink, source: .upper)
        try removeItem(volume: volume, item: upperDanglingRemoveItem, name: FSFileName(string: "upper-dangling-remove"), directory: workspace)
        guard !itemExistsNoFollow(upperDanglingRemove) else {
            throw SmokeError("removeItem did not remove upper-only dangling symlink")
        }
        let upperDanglingRenameItem = OSIxItem(relativePath: upperDanglingRenamePath, physicalPath: upperDanglingRename, type: .symlink, source: .upper)
        try renameItem(volume: volume, item: upperDanglingRenameItem, sourceDirectory: workspace, sourceName: FSFileName(string: "upper-dangling-rename"), destinationName: FSFileName(string: "upper-dangling-renamed"), destinationDirectory: workspace)
        guard !itemExistsNoFollow(upperDanglingRename) else {
            throw SmokeError("renameItem left upper-only dangling symlink source")
        }
        guard (try? FileManager.default.destinationOfSymbolicLink(atPath: upperDanglingRenameDestination)) == "missing-upper-rename-target" else {
            throw SmokeError("renameItem did not move upper-only dangling symlink")
        }
        let writeItem = OSIxItem(relativePath: writePath, physicalPath: lowerWriteFile, type: .file, source: .lower)
        try writeData(volume: volume, data: Data("XX".utf8), item: writeItem, offset: 2)
        guard (try? String(contentsOfFile: upperWriteFile, encoding: .utf8)) == "wrXXe" else {
            throw SmokeError("write did not copy lower file into upper and patch requested offset")
        }
        do {
            try writeData(volume: volume, data: Data("bad".utf8), item: writeItem, offset: -1)
            throw SmokeError("write accepted negative offset")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL) {
        }
        guard (try? String(contentsOfFile: upperWriteFile, encoding: .utf8)) == "wrXXe" else {
            throw SmokeError("negative-offset write mutated upper file")
        }
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            try writeData(volume: volume, data: Data("YY".utf8), item: writeItem, offset: 0)
            throw SmokeError("write succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard (try? String(contentsOfFile: upperWriteFile, encoding: .utf8)) == "wrXXe" else {
            throw SmokeError("failed write flush rollback did not restore upper file contents")
        }
        let cowDelete = OSIxItem(relativePath: cowDeletePath, physicalPath: lowerCOWDeleteFile, type: .file, source: .lower)
        let cowDeleteRequest = FSItem.SetAttributesRequest()
        cowDeleteRequest.mode = 0o600
        _ = try setAttributes(volume: volume, request: cowDeleteRequest, item: cowDelete)
        guard FileManager.default.fileExists(atPath: upperCOWDeleteFile) else {
            throw SmokeError("copy-on-write delete fixture did not create upper copy")
        }
        try removeItem(volume: volume, item: cowDelete, name: FSFileName(string: "cow-delete.txt"), directory: workspace)
        guard FileManager.default.fileExists(atPath: upperCOWDeleteWhiteout) else {
            throw SmokeError("removing COW upper copy did not whiteout lower item")
        }
        do {
            _ = try getAttributes(volume: volume, item: cowDelete)
            throw SmokeError("removing COW upper copy left lower item visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let removeFlushRollback = OSIxItem(relativePath: removeFlushRollbackPath, physicalPath: lowerRemoveFlushRollbackFile, type: .file, source: .lower)
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            try removeItem(volume: volume, item: removeFlushRollback, name: FSFileName(string: "remove-flush-rollback.txt"), directory: workspace)
            throw SmokeError("removeItem succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard (try? String(contentsOfFile: lowerRemoveFlushRollbackFile, encoding: .utf8)) == "remove flush rollback lower",
              (try? String(contentsOfFile: upperRemoveFlushRollbackFile, encoding: .utf8)) == "remove flush rollback upper",
              !FileManager.default.fileExists(atPath: upperRemoveFlushRollbackWhiteout) else {
            throw SmokeError("failed removeItem flush rollback did not restore COW source state")
        }
        _ = try lookupItem(volume: volume, name: FSFileName(string: "remove-flush-rollback.txt"), directory: workspace)
        let cowRename = OSIxItem(relativePath: cowRenamePath, physicalPath: lowerCOWRenameFile, type: .file, source: .lower)
        let cowRenameRequest = FSItem.SetAttributesRequest()
        cowRenameRequest.size = 3
        _ = try setAttributes(volume: volume, request: cowRenameRequest, item: cowRename)
        try renameItem(volume: volume, item: cowRename, sourceDirectory: workspace, sourceName: FSFileName(string: "cow-rename.txt"), destinationName: FSFileName(string: "cow-renamed.txt"), destinationDirectory: workspace)
        guard FileManager.default.fileExists(atPath: upperCOWRenameWhiteout) else {
            throw SmokeError("renaming COW upper copy did not whiteout lower source")
        }
        guard (try? String(contentsOfFile: upperCOWRenameDestination, encoding: .utf8)) == "cow" else {
            throw SmokeError("renaming COW upper copy did not move truncated upper content")
        }
        do {
            _ = try getAttributes(volume: volume, item: cowRename)
            throw SmokeError("renaming COW upper copy left lower source visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let renameHiddenRollback = OSIxItem(relativePath: renameHiddenRollbackSourcePath, physicalPath: lowerRenameHiddenRollbackSource, type: .file, source: .lower)
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            try renameItem(volume: volume, item: renameHiddenRollback, sourceDirectory: workspace, sourceName: FSFileName(string: "rename-hidden-rollback-source.txt"), destinationName: FSFileName(string: "rename-hidden-rollback-dest.txt"), destinationDirectory: workspace)
            throw SmokeError("rename over hidden whiteout succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard (try? String(contentsOfFile: lowerRenameHiddenRollbackSource, encoding: .utf8)) == "hidden rollback source",
              !itemExistsNoFollow(upperRenameHiddenRollbackSource),
              !FileManager.default.fileExists(atPath: upperRenameHiddenRollbackSourceWhiteout),
              (try? String(contentsOfFile: upperRenameHiddenRollbackDestination, encoding: .utf8)) == "hidden rename rollback",
              FileManager.default.fileExists(atPath: upperRenameHiddenRollbackDestinationWhiteout) else {
            throw SmokeError("failed rename over hidden whiteout did not restore source/destination state")
        }
        do {
            _ = try lookupItem(volume: volume, name: FSFileName(string: "rename-hidden-rollback-dest.txt"), directory: workspace)
            throw SmokeError("failed rename over hidden whiteout exposed hidden destination")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let staleUpperRename = OSIxItem(relativePath: staleRenamePath, physicalPath: URL(fileURLWithPath: upper).appendingPathComponent(staleRenamePath).path, type: .file, source: .upper)
        try renameItem(volume: volume, item: staleUpperRename, sourceDirectory: workspace, sourceName: FSFileName(string: "rename-me.txt"), destinationName: FSFileName(string: "renamed.txt"), destinationDirectory: workspace)
        guard FileManager.default.fileExists(atPath: upperRenameWhiteout) else {
            throw SmokeError("stale upper rename did not whiteout current lower item")
        }
        guard (try? String(contentsOfFile: upperRenameDestination, encoding: .utf8)) == "rename" else {
            throw SmokeError("stale upper rename did not copy current lower content to destination")
        }
        do {
            _ = try getAttributes(volume: volume, item: staleUpperRename)
            throw SmokeError("stale upper rename left lower source visible")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT) {
        }
        let createdName = FSFileName(string: "created.bin")
        let createdRequest = FSItem.SetAttributesRequest()
        createdRequest.size = 4
        createdRequest.mode = 0o604
        try createItem(volume: volume, name: createdName, type: .file, directory: workspace, attributes: createdRequest)
        let createdPath = URL(fileURLWithPath: upper).appendingPathComponent("agent/workspace/created.bin").path
        let createdAttributes = try FileManager.default.attributesOfItem(atPath: createdPath)
        let createdMode = (createdAttributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard createdRequest.wasAttributeConsumed(.size), createdRequest.wasAttributeConsumed(.mode) else {
            throw SmokeError("createItem did not consume requested size/mode attributes")
        }
        guard (createdAttributes[.size] as? NSNumber)?.intValue == 4, createdMode == 0o604 else {
            throw SmokeError(String(format: "created file metadata mismatch: size=%@ mode=%04o", String(describing: createdAttributes[.size]), createdMode))
        }

        let xattrName = FSFileName(string: "osix.smoke")
        try setXattr(volume: volume, name: xattrName, value: Data("one".utf8), item: item, policy: .alwaysSet)
        guard try getXattr(volume: volume, name: xattrName, item: item) == Data("one".utf8) else {
            throw SmokeError("getXattr did not read the upper copy")
        }
        guard try listXattrs(volume: volume, item: item).contains("osix.smoke") else {
            throw SmokeError("listXattrs did not include osix.smoke")
        }
        try? FileManager.default.removeItem(atPath: dirtyFile)
        try FileManager.default.createDirectory(atPath: dirtyFile, withIntermediateDirectories: false)
        do {
            try setXattr(volume: volume, name: xattrName, value: Data("rollback".utf8), item: item, policy: .alwaysSet)
            throw SmokeError("setXattr succeeded despite dirty flush failure")
        } catch let error as NSError where error.domain == NSPOSIXErrorDomain || error.domain == NSCocoaErrorDomain {
        }
        try FileManager.default.removeItem(atPath: dirtyFile)
        guard try getXattr(volume: volume, name: xattrName, item: item) == Data("one".utf8) else {
            throw SmokeError("failed setXattr flush rollback did not restore previous xattr value")
        }
        try setXattr(volume: volume, name: xattrName, value: Data("two".utf8), item: item, policy: .mustReplace)
        guard try getXattr(volume: volume, name: xattrName, item: item) == Data("two".utf8) else {
            throw SmokeError("setXattr mustReplace did not update the value")
        }
        try setXattr(volume: volume, name: xattrName, value: nil, item: item, policy: .delete)
        guard !((try? listXattrs(volume: volume, item: item).contains("osix.smoke")) ?? true) else {
            throw SmokeError("setXattr delete did not remove osix.smoke")
        }
        guard getxattr(lowerFile, "osix.smoke", nil, 0, 0, 0) < 0, errno == ENOATTR else {
            throw SmokeError("lower file gained xattr data")
        }

        let dirtyData = try Data(contentsOf: URL(fileURLWithPath: dirtyFile))
        let dirty = try JSONDecoder().decode(DirtyOutput.self, from: dirtyData)
        guard dirty.paths[relativePath] == "modified" else {
            throw SmokeError("dirty index did not mark \(relativePath) modified")
        }
        guard dirty.paths[staleDeletePath] == "deleted" else {
            throw SmokeError("dirty index did not mark stale delete path deleted")
        }
        guard dirty.paths[staleRenamePath] == "deleted" else {
            throw SmokeError("dirty index did not mark stale rename source deleted")
        }
        guard dirty.paths[staleRenameDestinationPath] == "modified" else {
            throw SmokeError("dirty index did not mark stale rename destination modified")
        }
        guard dirty.paths[nameBoundaryRemovePath] == "deleted" else {
            throw SmokeError("dirty index did not mark name-boundary remove path deleted")
        }
        guard dirty.paths[nameBoundaryRenamePath] == "deleted" else {
            throw SmokeError("dirty index did not mark name-boundary rename source deleted")
        }
        guard dirty.paths[nameBoundaryRenameDestinationPath] == "modified" else {
            throw SmokeError("dirty index did not mark name-boundary rename destination modified")
        }
        guard dirty.paths[renameOverLowerFileSourcePath] == "deleted" else {
            throw SmokeError("dirty index did not mark rename-over-lower-file source deleted")
        }
        guard dirty.paths[renameOverLowerFileDestinationPath] == "modified" else {
            throw SmokeError("dirty index did not mark rename-over-lower-file destination modified")
        }
        guard dirty.paths[cowDeletePath] == "deleted" else {
            throw SmokeError("dirty index did not mark COW delete path deleted")
        }
        guard dirty.paths[cowRenamePath] == "deleted" else {
            throw SmokeError("dirty index did not mark COW rename source deleted")
        }
        guard dirty.paths[cowRenameDestinationPath] == "modified" else {
            throw SmokeError("dirty index did not mark COW rename destination modified")
        }
        guard dirty.paths[renameLowerDirectoryPath] == "deleted" else {
            throw SmokeError("dirty index did not mark lower directory rename source deleted")
        }
        guard dirty.paths[renameLowerDirectoryDestinationPath] == "modified",
              dirty.paths[renameLowerDirectoryDestinationPath + "/lower-child.txt"] == "modified",
              dirty.paths[renameLowerDirectoryDestinationPath + "/upper-child.txt"] == "modified" else {
            throw SmokeError("dirty index did not mark lower directory rename destination tree modified")
        }
        guard dirty.paths[replaceWhiteoutPath] == "modified" else {
            throw SmokeError("dirty index did not mark create-over-whiteout path modified")
        }
        guard dirty.paths[upperDanglingRenameDestinationPath] == "modified" else {
            throw SmokeError("dirty index did not mark upper dangling symlink rename destination modified")
        }
        guard dirty.paths[writePath] == "modified" else {
            throw SmokeError("dirty index did not mark write path modified")
        }
        guard dirty.paths[danglingLinkPath] == "modified" else {
            throw SmokeError("dirty index did not mark copied-up dangling symlink modified")
        }
        guard dirty.paths["agent/workspace/created.bin"] == "modified" else {
            throw SmokeError("dirty index did not mark created file modified")
        }
    }

    struct DirtyOutput: Decodable {
        let paths: [String: String]
    }

    static func validateMountOptions(lower: String, upper: String, work: String) throws {
        let validDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        try OSIxMountOptions(
            bundle: nil,
            workspace: URL(fileURLWithPath: work).deletingLastPathComponent().path,
            sourceRef: "snap-000001",
            sourceDigest: validDigest,
            lower: lower,
            upper: upper,
            work: work,
            mode: "overlay"
        ).validateForMount()

        do {
            try OSIxMountOptions(bundle: nil, workspace: nil, sourceRef: "snap-000001", sourceDigest: "sha256:digest", lower: lower, upper: upper, work: work, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted missing workspace")
        } catch is OSIxMountOptionsValidationError {
        }

        let workspaceFile = URL(fileURLWithPath: work).deletingLastPathComponent().appendingPathComponent("workspace-file").path
        FileManager.default.createFile(atPath: workspaceFile, contents: Data("not a directory".utf8))
        do {
            try OSIxMountOptions(bundle: nil, workspace: workspaceFile, sourceRef: "snap-000001", sourceDigest: validDigest, lower: lower, upper: upper, work: work, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted non-directory workspace")
        } catch is OSIxMountOptionsValidationError {
        }

        do {
            try OSIxMountOptions(bundle: nil, workspace: work, sourceRef: "snap-000001", sourceDigest: "not-a-digest", lower: lower, upper: upper, work: work, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted malformed source digest")
        } catch is OSIxMountOptionsValidationError {
        }

        do {
            try OSIxMountOptions(bundle: nil, workspace: work, sourceRef: "snap-000001", sourceDigest: validDigest, lower: lower, upper: lower, work: work, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted aliased lower and upper directories")
        } catch is OSIxMountOptionsValidationError {
        }

        let nestedWork = URL(fileURLWithPath: upper).appendingPathComponent("nested-work").path
        try FileManager.default.createDirectory(atPath: nestedWork, withIntermediateDirectories: true)
        do {
            try OSIxMountOptions(bundle: nil, workspace: work, sourceRef: "snap-000001", sourceDigest: validDigest, lower: lower, upper: upper, work: nestedWork, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted nested upper/work directories")
        } catch is OSIxMountOptionsValidationError {
        }

        do {
            try OSIxMountOptions(bundle: nil, workspace: work, sourceRef: "snap-000001", sourceDigest: "sha256:digest", lower: lower, upper: upper, work: work, mode: "materialized").validateForMount()
            throw SmokeError("mount options accepted unsupported mode")
        } catch is OSIxMountOptionsValidationError {
        }

        let workSymlink = URL(fileURLWithPath: work).deletingLastPathComponent().appendingPathComponent("work-link").path
        try FileManager.default.createSymbolicLink(atPath: workSymlink, withDestinationPath: work)
        do {
            try OSIxMountOptions(bundle: nil, workspace: work, sourceRef: "snap-000001", sourceDigest: validDigest, lower: lower, upper: upper, work: workSymlink, mode: "overlay").validateForMount()
            throw SmokeError("mount options accepted symlink workdir")
        } catch is OSIxMountOptionsValidationError {
        }
    }

    static func setAttributes(volume: OSIxVolume, request: FSItem.SetAttributesRequest, item: FSItem) throws -> FSItem.Attributes {
        var replyAttributes: FSItem.Attributes?
        var replyError: (any Error)?
        volume.setAttributes(request, on: item) { attributes, error in
            replyAttributes = attributes
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let replyAttributes else {
            throw SmokeError("setAttributes returned no attributes")
        }
        return replyAttributes
    }

    static func createItem(volume: OSIxVolume, name: FSFileName, type: FSItem.ItemType, directory: FSItem, attributes: FSItem.SetAttributesRequest) throws {
        var replyItem: FSItem?
        var replyError: (any Error)?
        volume.createItem(named: name, type: type, inDirectory: directory, attributes: attributes) { item, _, error in
            replyItem = item
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard replyItem != nil else {
            throw SmokeError("createItem returned no item")
        }
    }

    static func createSymbolicLink(volume: OSIxVolume, name: FSFileName, directory: FSItem, contents: FSFileName, attributes: FSItem.SetAttributesRequest) throws {
        var replyItem: FSItem?
        var replyError: (any Error)?
        volume.createSymbolicLink(named: name, inDirectory: directory, attributes: attributes, linkContents: contents) { item, _, error in
            replyItem = item
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard replyItem != nil else {
            throw SmokeError("createSymbolicLink returned no item")
        }
    }

    static func workspaceItem(lower: String, relativePath: String) -> OSIxItem {
        OSIxItem(
            relativePath: relativePath,
            physicalPath: URL(fileURLWithPath: lower).appendingPathComponent(relativePath).path,
            type: .directory,
            source: .lower
        )
    }

    static func getAttributes(volume: OSIxVolume, item: FSItem) throws -> FSItem.Attributes {
        var replyAttributes: FSItem.Attributes?
        var replyError: (any Error)?
        volume.getAttributes(FSItem.GetAttributesRequest(), of: item) { attributes, error in
            replyAttributes = attributes
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let replyAttributes else {
            throw SmokeError("getAttributes returned no attributes")
        }
        return replyAttributes
    }

    static func lookupItem(volume: OSIxVolume, name: FSFileName, directory: FSItem) throws -> FSItem {
        var replyItem: FSItem?
        var replyError: (any Error)?
        volume.lookupItem(named: name, inDirectory: directory) { item, _, error in
            replyItem = item
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let replyItem else {
            throw SmokeError("lookupItem returned no item")
        }
        return replyItem
    }

    static func readSymbolicLink(volume: OSIxVolume, item: FSItem) throws -> String {
        var replyDestination: FSFileName?
        var replyError: (any Error)?
        volume.readSymbolicLink(item) { destination, error in
            replyDestination = destination
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let destination = replyDestination?.string else {
            throw SmokeError("readSymbolicLink returned no destination")
        }
        return destination
    }

    static func removeItem(volume: OSIxVolume, item: FSItem, name: FSFileName, directory: FSItem) throws {
        var replyError: (any Error)?
        volume.removeItem(item, named: name, fromDirectory: directory) { error in
            replyError = error
        }
        if let replyError {
            throw replyError
        }
    }

    static func renameItem(volume: OSIxVolume, item: FSItem, sourceDirectory: FSItem, sourceName: FSFileName, destinationName: FSFileName, destinationDirectory: FSItem) throws {
        var replyError: (any Error)?
        volume.renameItem(item, inDirectory: sourceDirectory, named: sourceName, to: destinationName, inDirectory: destinationDirectory, overItem: nil) { _, error in
            replyError = error
        }
        if let replyError {
            throw replyError
        }
    }

    static func writeData(volume: OSIxVolume, data: Data, item: FSItem, offset: off_t) throws {
        var replyCount = 0
        var replyError: (any Error)?
        volume.write(contents: data, to: item, at: offset) { count, error in
            replyCount = count
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard replyCount == data.count else {
            throw SmokeError("write returned \(replyCount) bytes, want \(data.count)")
        }
    }

    static func getXattr(volume: OSIxVolume, name: FSFileName, item: FSItem) throws -> Data {
        var replyValue: Data?
        var replyError: (any Error)?
        volume.getXattr(named: name, of: item) { value, error in
            replyValue = value
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        guard let replyValue else {
            throw SmokeError("getXattr returned no data")
        }
        return replyValue
    }

    static func setXattr(volume: OSIxVolume, name: FSFileName, value: Data?, item: FSItem, policy: FSVolume.SetXattrPolicy) throws {
        var replyError: (any Error)?
        volume.setXattr(named: name, to: value, on: item, policy: policy) { error in
            replyError = error
        }
        if let replyError {
            throw replyError
        }
    }

    static func getRawXattr(path: String, name: String, options: Int32) throws -> Data {
        let size = getxattr(path, name, nil, 0, 0, options)
        if size < 0 {
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
        }
        var buffer = [UInt8](repeating: 0, count: size)
        let readSize = getxattr(path, name, &buffer, buffer.count, 0, options)
        if readSize < 0 {
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
        }
        return Data(buffer.prefix(readSize))
    }

    static func setRawXattr(path: String, name: String, value: Data, options: Int32) throws {
        let status = value.withUnsafeBytes { rawBuffer in
            setxattr(path, name, rawBuffer.baseAddress, value.count, 0, options)
        }
        if status != 0 {
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
        }
    }

    static func listXattrs(volume: OSIxVolume, item: FSItem) throws -> [String] {
        var replyNames: [FSFileName]?
        var replyError: (any Error)?
        volume.listXattrs(of: item) { names, error in
            replyNames = names
            replyError = error
        }
        if let replyError {
            throw replyError
        }
        return (replyNames ?? []).compactMap(\.string)
    }
}

struct SmokeError: Error, CustomStringConvertible {
    let description: String

    init(_ description: String) {
        self.description = description
    }
}

func itemExistsNoFollow(_ path: String) -> Bool {
    var statBuffer = stat()
    return lstat(path, &statBuffer) == 0
}
