//go:build darwin && cgo

package auth

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef glab_axi_string(const char *value) {
    return CFStringCreateWithBytes(kCFAllocatorDefault,
        (const UInt8 *)value, (CFIndex)strlen(value),
        kCFStringEncodingUTF8, false);
}

static void glab_axi_query_value(CFMutableDictionaryRef query,
                                 const void *key, const void *value) {
    CFDictionarySetValue(query, key, value);
}

static CFMutableDictionaryRef glab_axi_query(const char *service,
                                              const char *account) {
    CFStringRef serviceRef = glab_axi_string(service);
    CFStringRef accountRef = glab_axi_string(account);
    if (serviceRef == NULL || accountRef == NULL) {
        if (serviceRef != NULL) CFRelease(serviceRef);
        if (accountRef != NULL) CFRelease(accountRef);
        return NULL;
    }
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (query != NULL) {
        glab_axi_query_value(query, kSecClass, kSecClassGenericPassword);
        glab_axi_query_value(query, kSecAttrService, serviceRef);
        glab_axi_query_value(query, kSecAttrAccount, accountRef);
        glab_axi_query_value(query, kSecUseAuthenticationUI,
                             kSecUseAuthenticationUIFail);
    }
    CFRelease(serviceRef);
    CFRelease(accountRef);
    return query;
}

static OSStatus glab_axi_get(const char *service, const char *account,
                             size_t *length, void **data) {
    *length = 0;
    *data = NULL;
    CFMutableDictionaryRef query = glab_axi_query(service, account);
    if (query == NULL) return errSecAllocate;
    glab_axi_query_value(query, kSecReturnData, kCFBooleanTrue);
    glab_axi_query_value(query, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status != errSecSuccess) return status;
    if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
        if (result != NULL) CFRelease(result);
        return errSecDecode;
    }
    CFDataRef secret = (CFDataRef)result;
    CFIndex size = CFDataGetLength(secret);
    if (size < 0) {
        CFRelease(result);
        return errSecDecode;
    }
    void *copy = malloc((size_t)size);
    if (copy == NULL && size > 0) {
        CFRelease(result);
        return errSecAllocate;
    }
    if (size > 0) memcpy(copy, CFDataGetBytePtr(secret), (size_t)size);
    *length = (size_t)size;
    *data = copy;
    CFRelease(result);
    return errSecSuccess;
}

static OSStatus glab_axi_set(const char *service, const char *account,
                             const void *secret, size_t length) {
    CFMutableDictionaryRef query = glab_axi_query(service, account);
    if (query == NULL) return errSecAllocate;
    CFDataRef data = CFDataCreate(kCFAllocatorDefault,
        (const UInt8 *)secret, (CFIndex)length);
    if (data == NULL) {
        CFRelease(query);
        return errSecAllocate;
    }
    CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (attributes == NULL) {
        CFRelease(data);
        CFRelease(query);
        return errSecAllocate;
    }
    glab_axi_query_value(attributes, kSecValueData, data);
    OSStatus status = SecItemUpdate(query, attributes);
    CFRelease(attributes);
    if (status == errSecItemNotFound) {
        glab_axi_query_value(query, kSecValueData, data);
        status = SecItemAdd(query, NULL);
    }
    CFRelease(data);
    CFRelease(query);
    return status;
}

static OSStatus glab_axi_delete(const char *service, const char *account) {
    CFMutableDictionaryRef query = glab_axi_query(service, account);
    if (query == NULL) return errSecAllocate;
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}

static void glab_axi_zero(void *data, size_t length) {
    if (data != NULL && length > 0) memset(data, 0, length);
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

type systemKeyring struct{}

func (systemKeyring) Get(ctx context.Context, service, account string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	var length C.size_t
	var data unsafe.Pointer
	status := C.glab_axi_get(cs, ca, &length, &data)
	if status == C.errSecItemNotFound {
		return "", ErrKeyringNotFound
	}
	if status != C.errSecSuccess {
		return "", fmt.Errorf("%w: OSStatus %d", ErrKeyringUnavailable, int(status))
	}
	defer func() {
		C.glab_axi_zero(data, length)
		C.free(data)
	}()
	return C.GoStringN((*C.char)(data), C.int(length)), nil
}

func (systemKeyring) Set(ctx context.Context, service, account, secret string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cs := C.CString(service)
	ca := C.CString(account)
	data := C.CBytes([]byte(secret))
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	defer func() {
		C.glab_axi_zero(data, C.size_t(len(secret)))
		C.free(data)
	}()
	status := C.glab_axi_set(cs, ca, data, C.size_t(len(secret)))
	if status != C.errSecSuccess {
		return fmt.Errorf("%w: OSStatus %d", ErrKeyringUnavailable, int(status))
	}
	return nil
}

func (systemKeyring) Delete(ctx context.Context, service, account string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	status := C.glab_axi_delete(cs, ca)
	if status == C.errSecItemNotFound {
		return nil
	}
	if status != C.errSecSuccess {
		return fmt.Errorf("%w: OSStatus %d", ErrKeyringUnavailable, int(status))
	}
	return nil
}
