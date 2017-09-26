#include <stdio.h>
#include <unistd.h>

#include <librealsense/rs.hpp>
#include <opencv2/opencv.hpp>

const int kSkipFirstFrames = 60;

void fail(const char* msg) {
  fprintf(stderr, "%s\n", msg);
  _exit(1);
}

int main(void) {
  rs::log_to_console(rs::log_severity::warn);

  // Create a context.
  rs::context ctx;

  // Find and open a device.
  if (!ctx.get_device_count()) {
    fail("No RealSense devices connected");
  }

  rs::device * dev = ctx.get_device(0);
  fprintf(stderr, "RealSense device opened: %s, SN %s, firmware version %s\n",
	  dev->get_name(), dev->get_serial(), dev->get_firmware_version());

  // Check that the camera supports color and depth streams and enable them.
  if (!dev->supports(rs::capabilities::color)) {
    fail("Device does not support RGB stream");
  }
  if (!dev->supports(rs::capabilities::depth)) {
    fail("Device does not support Depth stream");
  }

  dev->enable_stream(rs::stream::color, rs::preset::best_quality);
  dev->enable_stream(rs::stream::depth, rs::preset::best_quality);

  // Start streaming.
  dev->start();

  // Get stream params (including width and height of each frame).
  rs::intrinsics color_intrinsics = dev->get_stream_intrinsics(rs::stream::color);
  rs::intrinsics depth_intrinsics = dev->get_stream_intrinsics(rs::stream::depth);

  // Skip first few frames to make sure we have a stable image.
  for (int i = 0; i < kSkipFirstFrames; i++) {
    dev->wait_for_frames();
  }

  // TODO(krasin): get actual data from the streams.

  return 0;
}
