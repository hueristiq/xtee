# xtee

![made with go](https://img.shields.io/badge/made%20with-Go-1E90FF.svg) [![go report card](https://goreportcard.com/badge/github.com/hueristiq/xtee)](https://goreportcard.com/report/github.com/hueristiq/xtee) [![release](https://img.shields.io/github/release/hueristiq/xtee?style=flat&color=1E90FF)](https://github.com/hueristiq/xtee/releases) [![open issues](https://img.shields.io/github/issues-raw/hueristiq/xtee.svg?style=flat&color=1E90FF)](https://github.com/hueristiq/xtee/issues?q=is:issue+is:open) [![closed issues](https://img.shields.io/github/issues-closed-raw/hueristiq/xtee.svg?style=flat&color=1E90FF)](https://github.com/hueristiq/xtee/issues?q=is:issue+is:closed) [![license](https://img.shields.io/badge/license-MIT-gray.svg?color=1E90FF)](https://github.com/hueristiq/xtee/blob/master/LICENSE) ![maintenance](https://img.shields.io/badge/maintained%3F-yes-1E90FF.svg) [![contribution](https://img.shields.io/badge/contributions-welcome-1E90FF.svg)](https://github.com/hueristiq/xtee/blob/master/CONTRIBUTING.md)

`xtee` is a command-line utility for reading from standard input and writing to both standard output and file, providing powerful features for file manipulation and data processing. It combines the functionality of [`tee`](https://github.com/coreutils/coreutils/blob/master/src/tee.c), the soaking behavior of [`sponge`](https://github.com/pgdr/moreutils/blob/master/sponge.c), and more.

## Resources

- [Features](#features)
- [Installation](#installation)
	- [Install release binaries (Without Go Installed)](#install-release-binaries-without-go-installed)
	- [Install source (With Go Installed)](#install-source-with-go-installed)
		- [`go install ...`](#go-install)
		- [`go build ...` the development Version](#go-build--the-development-version)
- [Usage](#usage)
	- [Examples](#examples)
		- [Appending Unique Lines to File](#appending-unique-lines-to-file)
		- [Deduplicating Files](#deduplicating-files)
- [Contributing](#contributing)
- [Licensing](#licensing)

## Features

- Splits incoming standard input into standard output and file
- Supports soaking up input before writing to output file
- Supports appending and overwriting outputs
- Supports deduplication
- Cross-Platform (Windows, Linux & macOS)

## Installation

### Install release binaries (without Go installed)

Visit the [releases page](https://github.com/hueristiq/xtee/releases) and find the appropriate archive for your operating system and architecture. Download the archive from your browser or copy its URL and retrieve it with `wget` or `curl`:

- ...with `wget`:

	```bash
	wget https://github.com/hueristiq/xtee/releases/download/v<version>/xtee-<version>-linux-amd64.tar.gz
	```

- ...or, with `curl`:

	```bash
	curl -OL https://github.com/hueristiq/xtee/releases/download/v<version>/xtee-<version>-linux-amd64.tar.gz
	```

...then, extract the binary:

```bash
tar xf xtee-<version>-linux-amd64.tar.gz
```

> [!TIP]
> The above steps, download and extract, can be combined into a single step with this onliner
> 
> ```bash
> curl -sL https://github.com/hueristiq/xtee/releases/download/v<version>/xtee-<version>-linux-amd64.tar.gz | tar -xzv
> ```

> [!NOTE]
> On Windows systems, you should be able to double-click the zip archive to extract the `xtee` executable.

...move the `xtee` binary to somewhere in your `PATH`. For example, on GNU/Linux and OS X systems:

```bash
sudo mv xtee /usr/local/bin/
```

> [!NOTE]
> Windows users can follow [How to: Add Tool Locations to the PATH Environment Variable](https://msdn.microsoft.com/en-us/library/office/ee537574(v=office.14).aspx) in order to add `xtee` to their `PATH`.

### Install source (with Go installed)

Before you install from source, you need to make sure that Go is installed on your system. You can install Go by following the official instructions for your operating system. For this, we will assume that Go is already installed.

#### `go install ...`

```bash
go install -v github.com/hueristiq/xtee/cmd/xtee@latest
```

#### `go build ...` the development version

- Clone the repository

	```bash
	git clone https://github.com/hueristiq/xtee.git 
	```

- Build the utility

	```bash
	cd xtee/cmd/xtee && \
	go build .
	```

- Move the `xtee` binary to somewhere in your `PATH`. For example, on GNU/Linux and OS X systems:

	```bash
	sudo mv xtee /usr/local/bin/
	```

	Windows users can follow [How to: Add Tool Locations to the PATH Environment Variable](https://msdn.microsoft.com/en-us/library/office/ee537574(v=office.14).aspx) in order to add `xtee` to their `PATH`.


> [!CAUTION]
> While the development version is a good way to take a peek at `xtee`'s latest features before they get released, be aware that it may have bugs. Officially released versions will generally be more stable.

## Usage

To display help message for `xtee` use the `-h` flag:

```bash
xtee -h
```

help message:

```text

      _
__  _| |_ ___  ___
\ \/ / __/ _ \/ _ \
 >  <| ||  __/  __/
/_/\_\\__\___|\___|
             v0.0.0

USAGE:
 xtee [OPTION]... <FILE>

INPUT:
     --soak bool          soak up all input before writing to file

OUTPUT:
 -a, --append bool        append lines to output
 -u, --unique bool        output unique lines
 -p, --preview bool       preview new lines, without writing to file
 -q, --quiet bool         suppress output to stdout
     --monochrome bool    display no color output

```

### Examples

#### Appending Unique Lines to File

```bash
cat stuff.txt
```

```
one
two
three
```

```bash
cat new-stuff.txt
```

```
zero
one
two
three
four
five
```

```bash
cat new-stuff.txt | xtee stuff.txt --append --unique
```

```
zero
four
five
```

```bash
cat stuff.txt
```

```
one
two
three
zero
four
five
```

Note that the new lines added to `stuff.txt` are also sent to `stdout`, this allows for them to be redirected to another file:

```bash
cat new-stuff.txt | xtee stuff.txt --append --unique > added-lines.txt
```

```bash
cat added-lines.txt
```

```
zero
four
five
```

#### Deduplicating Files

```bash
cat stuff.txt
```

```
zero
one
two
three
zero
four
five
five
```

```bash
cat stuff.txt | xtee stuff.txt --soak --unique
```

```
zero
one
two
three
four
five
```

```bash
cat stuff.txt
```

```
zero
one
two
three
four
five
```

Note the use of `--soak`, it makes the utility soak up all its input before writing to a file. This is useful for reading from and writing to the same file in a single pipeline.

## Contributing

Contributions are welcome and encouraged! Feel free to submit [Pull Requests](https://github.com/hueristiq/xtee/pulls) or report [Issues](https://github.com/hueristiq/xtee/issues). For more details, check out the [contribution guidelines](https://github.com/hueristiq/xtee/blob/master/CONTRIBUTING.md).

A big thank you to all the [contributors](https://github.com/hueristiq/xtee/graphs/contributors) for your ongoing support!

![contributors](https://contrib.rocks/image?repo=hueristiq/xtee&max=500)

## Licensing

This package is licensed under the [MIT license](https://opensource.org/license/mit). You are free to use, modify, and distribute it, as long as you follow the terms of the license. You can find the full license text in the repository - [Full MIT license text](https://github.com/hueristiq/xtee/blob/master/LICENSE).