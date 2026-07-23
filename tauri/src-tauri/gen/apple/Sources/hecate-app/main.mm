#include "bindings/bindings.h"
#include "HecatePushBridge.h"

int main(int argc, char * argv[]) {
	HecatePushBootstrap();
	ffi::start_app();
	return 0;
}
