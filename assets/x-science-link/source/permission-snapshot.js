export function countAuthorizedLocalFolders(localFiles) {
  if (!localFiles || typeof localFiles !== 'object') return 0
  if (typeof localFiles.authorizedFolders === 'number') {
    return Math.max(0, localFiles.authorizedFolders)
  }
  if (Array.isArray(localFiles.authorizedFolders)) {
    return localFiles.authorizedFolders.length
  }
  if (Array.isArray(localFiles.folders)) {
    return localFiles.folders.length
  }
  return 0
}

export function buildPermissionSnapshot({ bookmarks, localFiles }) {
  const authorizedFolders = countAuthorizedLocalFolders(localFiles)
  return {
    bookmarks: Boolean(bookmarks),
    localFiles: authorizedFolders > 0,
    authorizedFolders,
    downloads: true,
  }
}
