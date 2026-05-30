package com.acidtv.unsyncthing

// Supported in-app preview kinds. Text/code is the only one for now; future
// kinds (images, PDFs, …) get a new entry here, an extension set in
// [Previewers], and a render branch in PreviewFragment.
enum class PreviewType { TEXT }

// Largest file we'll fetch for preview. The Go layer has no byte-range fetch,
// so the whole file is downloaded — cap it so previews stay cheap. Bigger files
// can still be saved via the download action.
const val MAX_PREVIEW_BYTES = 5L * 1024 * 1024

// Decides whether a file is previewable based purely on its extension. Kept as
// a pure function (no Android deps) so it's unit-testable on the JVM.
object Previewers {

    // Extensions we treat as UTF-8 text/code. Compared case-insensitively.
    private val TEXT_EXTENSIONS = setOf(
        "txt", "text", "md", "markdown", "rst", "log",
        "json", "xml", "yaml", "yml", "csv", "tsv",
        "ini", "conf", "cfg", "properties", "toml", "env",
        "gitignore", "gitattributes", "editorconfig",
        "sh", "bash", "zsh", "fish", "bat", "ps1",
        "py", "rb", "pl", "php", "lua", "r",
        "go", "rs", "swift", "kt", "kts", "java", "scala", "groovy", "gradle",
        "c", "h", "cpp", "cxx", "cc", "hpp", "hxx",
        "cs", "m", "mm",
        "js", "mjs", "cjs", "ts", "tsx", "jsx", "vue",
        "html", "htm", "css", "scss", "sass", "less",
        "sql", "graphql", "proto",
        "tex", "srt", "vtt", "diff", "patch",
    )

    // Filenames (no extension) that are conventionally text. Compared
    // case-insensitively against the basename.
    private val TEXT_FILENAMES = setOf(
        "readme", "license", "licence", "makefile", "dockerfile",
        "changelog", "authors", "notice", "copying", "todo",
    )

    fun typeFor(entry: FileEntry): PreviewType? = typeFor(entry.name)

    fun typeFor(name: String): PreviewType? {
        val basename = name.substringAfterLast('/').substringAfterLast('\\')
        val ext = basename.substringAfterLast('.', "").lowercase()
        if (ext in TEXT_EXTENSIONS) return PreviewType.TEXT
        if (basename.lowercase() in TEXT_FILENAMES) return PreviewType.TEXT
        return null
    }
}
