// Package dar reads Daml DAR archives and surfaces package-level
// metadata for `canton-devkit localnet dar info` and `dar diff`.
//
// A DAR is a ZIP file with this layout:
//
//	META-INF/MANIFEST.MF             — RFC822-style key/value with line-continuations
//	<main-package-name>-<hash>/
//	  <main-package-name>-<hash>.dalf            — main Daml-LF archive
//	  daml-prim-<hash>.dalf                       — stdlib dependencies
//	  daml-stdlib-<...>-<hash>.dalf
//	  <other-pkg-name>-<version>-<hash>.dalf     — other deps
//
// Each .dalf is a serialised Daml-LF Archive envelope (protobuf):
//
//	message Archive {
//	  HashFunction hash_function = 1;
//	  bytes        payload       = 2;  // serialised ArchivePayload
//	  string       hash          = 3;  // package id (hex of SHA256 of payload)
//	}
//	message ArchivePayload {
//	  string minor = 1;
//	  oneof Sum {
//	    DamlLf1 daml_lf_1 = 1;
//	    DamlLf2 daml_lf_2 = 2;
//	  }
//	}
//
// V1 of this package deliberately does NOT decode the inner DamlLf{1,2}
// payload — the schemas are not publicly published in the digital-asset/daml
// repo's `main` branch and a faithful pure-Go re-implementation is a
// months-long subproject. Templates / choices / fields with types are
// covered by the follow-up issue. What V1 surfaces:
//
//   - package id (from the .dalf filename hash and verified against the
//     archive envelope's `hash` field)
//   - package name + version (from the .dalf filename and DAR manifest)
//   - Daml-LF major.minor version (from the envelope's `minor` field +
//     which oneof tag is set)
//   - dependency graph (which packages reference which)
//   - SHA256 of each .dalf for tamper detection
//   - SDK version (from MANIFEST.MF's `Sdk-Version`)
//
// That set is enough for `dar info`, `dar diff` (with SCU-relevant
// signals at the package-level), and the dependency surfaces the
// remaining DAR-tooling tickets (BIT-50/51/53/55) will need.
package dar
