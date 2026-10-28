// Package compat keeps the sxops binary loadable on older Linux systems
// (Ubuntu 22.04 ships glibc 2.35).
//
// Background: GLFW's C sources are compiled with -D_GNU_SOURCE. On
// glibc 2.38+ headers that redirect strtol/strtoul/sscanf to the new
// __isoc23_* variants, so building sxops on a newer build host would
// silently produce a binary that refuses to load on glibc < 2.38 with
// "version GLIBC_2.38 not found" - and, when launched through the
// hidden Windows shortcut, fails completely invisibly. This file
// defines the three __isoc23_* functions locally (compiled WITHOUT
// _GNU_SOURCE, so they call the classic functions that exist since
// glibc 2.2.5), which removes the versioned imports from the binary.
package compat

/*
#include <stdlib.h>
#include <stdio.h>
#include <stdarg.h>

#ifndef _GNU_SOURCE

long __isoc23_strtol(const char *nptr, char **endptr, int base) {
	return strtol(nptr, endptr, base);
}

unsigned long __isoc23_strtoul(const char *nptr, char **endptr, int base) {
	return strtoul(nptr, endptr, base);
}

int __isoc23_sscanf(const char *str, const char *fmt, ...) {
	va_list ap;
	int rc;
	va_start(ap, fmt);
	rc = vsscanf(str, fmt, ap);
	va_end(ap);
	return rc;
}

#endif
*/
import "C"
