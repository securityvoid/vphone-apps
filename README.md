# vphone-apps

`vphone-apps` searches the App Store, downloads an app, removes its FairPlay encryption and installs it on a running [vphone](https://github.com/Lakr233/vphone-cli) virtual iPhone, in one command utilizing existing open source projects.

## Lawful use

Please be mindful to only use vphone-apps for legal and lawful purposes permitted by your locality. Consult your attorney if you have any questions on what this does or does not include. Vphone-apps is not responsible for your. misuse.

## Built on
Inspired by the [Darkwirelabs](https://www.darkwirelabs.com) article, ["From an encrypted App Store app to a running app on vphone”](https://www.darkwirelabs.com/articles/from-app-store-to-virtual-iphone-with-vphone/), 
vphone-apps assembles several open-source tools together to make it easier to install iOS Apps from the AppStore onto a virtualized iPhone running on [vphone-cli](https://github.com/Lakr233/vphone-cli). The hard work was all done by the projects relied upon. vphone-apps is just an interface that makes the process seamless:

- **[ipatool](https://github.com/majd/ipatool)** by Majd Alfhaily, MIT. Provides App Store login, search, license acquisition and IPA download. Used as a Go library. 
- **[unfairplay](https://github.com/DrWhax/unfairplay)** by DrWhax and contributors, MIT with Apache-2.0 (DarkPhoenix) and MIT (macOSFairFree) components. Provides one-time enrollment and FairPlay decryption of the IPA. A [PR has been submitted to restructure the Go project so that it can be used as a library](https://github.com/DrWhax/unfairplay/pull/28). In the meantime vphone-apps [uses a fork](https://github.com/securityvoid/unfairplay) which implements this PR.
- [**organicmaps**](https://github.com/organicmaps/organicmaps) by biodranik, Apache-2.0. Utilized as a sample application to enable unfairplay to decrypt applications. It is added, and cleaned up as part of the setup process.  
- **[vphone-cli](https://github.com/Lakr233/vphone-cli) and its guest daemon vphoned** by Lakr233, MIT. Provide the virtual iPhone and its install and launch services. Used through the documented `vphone.sock` control protocol.


Full license texts: `vphone-apps --licenses` (also the `NOTICE` file).

## Usage

```
vphone-apps login                         sign in to the App Store (session shared with ipatool);
                                          runs setup automatically when no model exists
vphone-apps search <term...>              search the App Store
vphone-apps install <term...> --vm <name> download, decrypt and install an app on a VM
vphone-apps install --id <bundleId> --vm <name>
        [--purchase] [--keep-account-data] [--keep] [--launch]
vphone-apps setup [--app <path.app> --ipa <path.ipa>] [--interface <name>] [--keep-work] [--verbose]
                                          one-time unfairplay enrollment (asks for sudo)
vphone-apps doctor [--vm <name>]          preflight: session, model, VM and apps.install
vphone-apps --licenses                    show third-party licenses
```

Sign-in and sudo prompts go to stderr:

```
Apple Account email: 
Apple Account password: 
Apple 2FA code: 
Mac login password (for sudo): 
```

macOS may ask once for access to the keychain item; choose Always Allow.

Example:

```
$ vphone-apps search organic maps
NAME          ID               VERSION  PRICE
Organic Maps  app.organicmaps  2026.10  Free
Install one with: vphone-apps install --id <ID> --vm <name>

$ vphone-apps install --id app.organicmaps --vm v1 --launch
Installing: Organic Maps (app.organicmaps) 2026.10
[1/5] Downloading
[=============               ] 45%
...
[5/5] Launching
[==============================] 100%
installed: app.organicmaps
launched: app.organicmaps pid 1234
```

## Build

Supported platform: macOS on Apple Silicon (arm64) only. Needs Go 1.26.5 with cgo (Xcode command line tools).

```
go build ./cmd/vphone-apps
```

## License

`vphone-apps` is released under the MIT License; see [LICENSE](LICENSE). Third-party licenses: `vphone-apps --licenses`.
