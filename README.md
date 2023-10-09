# `cl`

`cl` runs [`capslock`](https://github.com/google/capslock) on all imports in your module.

```
Usage of cl:
  -capability_map string
    	use a custom capability map file
  -disable_builtin
    	disable the builtin capability mappings when using a custom capability map
  -goarch string
    	GOARCH to use for analysis
  -goos string
    	GOOS to use for analysis
  -i value
    	imported package path patterns to ignore (allows multiple instances)
  -imports
    	list imports that would be analysed and then exit
  -lock
    	write out a new lock file
  -mod
    	include the whole main module (default true)
  -stdlib
    	include stdlib packages in analysis
  -v	print verbose output
```

When invoked with `-lock` a lock file and summary description are written to the root of the module or the current directory depending on `-mod`. Without `-lock` an existing lock file is compared to the current state of the module or tree.

`cl` requires that `capslock` is installed and in your `$PATH`.