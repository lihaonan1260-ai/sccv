// SPDX-License-Identifier: MIT

// Package i18n provides the minimal translation layer used to localize sccv.
//
// Keys are the original Chinese strings embedded in the code, so the Chinese
// text doubles as the default copy and needs no dictionary of its own:
//
//   - Lang "zh": T returns the key unchanged.
//   - Lang "en": T returns the English translation registered for the key,
//     or the key itself when none was registered. "en" is also the package
//     default so the library and every test linking it see stable English
//     semantics until an entrypoint calls SetLang with the user's choice.
//   - Any other Lang: T falls back to the key itself, exactly like a missing
//     translation, so an unregistered language degrades to Chinese instead of
//     empty output.
//
// Dictionaries are plain key/value maps registered per language via
// RegisterDict, meant to be called from the init() of one file per domain
// (dict_en_cli.go, dict_en_tabular.go, ...). Registration is additive and may
// happen any number of times; when two dictionaries claim the same key the
// first registration wins and later duplicates are ignored. Each domain owns
// its own keys, so conflicts should never happen in practice, and the
// first-wins rule keeps the outcome deterministic regardless of init order.
package i18n

var (
	// Lang is the active language. It defaults to "en" (library/test
	// semantics); the entrypoints set it explicitly with SetLang.
	Lang = "en"

	// dicts maps a language to its registered key->translation dictionary.
	dicts = map[string]map[string]string{}
)

// SetLang switches the language T translates into. Intended to be called once
// from the entrypoints with the user's choice.
func SetLang(language string) {
	Lang = language
}

// RegisterDict merges dict into the translations of language. Merging is
// additive so each domain file can register independently; the first
// registration of a key wins and later duplicates are ignored.
func RegisterDict(language string, dict map[string]string) {
	merged, ok := dicts[language]
	if !ok {
		merged = map[string]string{}
		dicts[language] = merged
	}
	for key, value := range dict {
		if _, taken := merged[key]; !taken {
			merged[key] = value
		}
	}
}

// T translates key into the active language. Because keys are the Chinese
// source strings, zh returns them unchanged and every other language looks up
// its dictionary, falling back to the key itself when no translation is
// registered.
func T(key string) string {
	if Lang == "zh" {
		return key
	}
	if translation, ok := dicts[Lang][key]; ok {
		return translation
	}
	return key
}
