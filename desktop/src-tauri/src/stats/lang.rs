/// The language of a file path for the stats (ADR 0059), or "Other" when
/// the path has no known extension.
pub fn language_of(path: &str) -> &'static str {
    let name = path.rsplit(['/', '\\']).next().unwrap_or(path);
    match name {
        "Makefile" | "GNUmakefile" => return "Make",
        _ if name == "Dockerfile" || name.starts_with("Dockerfile.") => return "Dockerfile",
        _ => {}
    }
    let Some((stem, extension)) = name.rsplit_once('.') else {
        return "Other";
    };
    if stem.is_empty() {
        return "Other";
    }
    match extension.to_ascii_lowercase().as_str() {
        "go" => "Go",
        "rs" => "Rust",
        "ts" | "tsx" | "mts" | "cts" => "TypeScript",
        "js" | "jsx" | "mjs" | "cjs" => "JavaScript",
        "py" => "Python",
        "rb" => "Ruby",
        "java" => "Java",
        "kt" | "kts" => "Kotlin",
        "swift" => "Swift",
        "c" | "h" => "C",
        "cc" | "cpp" | "cxx" | "hpp" | "hh" => "C++",
        "cs" => "C#",
        "php" => "PHP",
        "sh" | "bash" | "zsh" => "Shell",
        "sql" => "SQL",
        "html" | "htm" => "HTML",
        "css" | "scss" | "sass" | "less" => "CSS",
        "md" | "mdx" => "Markdown",
        "json" => "JSON",
        "yaml" | "yml" => "YAML",
        "toml" => "TOML",
        "proto" => "Protobuf",
        "ipynb" => "Notebook",
        "tf" | "hcl" => "HCL",
        "mk" => "Make",
        _ => "Other",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn typescript_jsx_extension_is_typescript() {
        assert_eq!(language_of("src/App.tsx"), "TypeScript");
    }

    #[test]
    fn module_javascript_extension_is_javascript() {
        assert_eq!(language_of("build/index.mjs"), "JavaScript");
    }

    #[test]
    fn cpp_header_extension_is_cpp() {
        assert_eq!(language_of("include/widget.hpp"), "C++");
    }

    #[test]
    fn yml_extension_is_yaml() {
        assert_eq!(language_of("ci/pipeline.yml"), "YAML");
    }

    #[test]
    fn extension_is_case_insensitive() {
        assert_eq!(language_of("cmd/MAIN.GO"), "Go");
    }

    #[test]
    fn makefile_is_make() {
        assert_eq!(language_of("repo/Makefile"), "Make");
    }

    #[test]
    fn dockerfile_variant_is_dockerfile() {
        assert_eq!(language_of("deploy/Dockerfile.dev"), "Dockerfile");
    }

    #[test]
    fn dotfile_without_extension_is_other() {
        assert_eq!(language_of("home/.bashrc"), "Other");
    }

    #[test]
    fn file_without_extension_is_other() {
        assert_eq!(language_of("bin/tool"), "Other");
    }

    #[test]
    fn windows_path_uses_its_base_name() {
        assert_eq!(language_of("C:\\work\\lib.rs"), "Rust");
    }
}
