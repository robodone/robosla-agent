#include <stdio.h>
#include <unistd.h>

#include <iostream>
#include <string>
#include <vector>

#include <librealsense2/rs.hpp>
#include <opencv2/opencv.hpp>

const int kSkipFirstFrames = 60;

void fail(const char* msg) {
  fprintf(stderr, "%s\n", msg);
  _exit(1);
}

float get_depth_scale(const rs2::device &dev) {
  for (rs2::sensor& sensor : dev.query_sensors()) {
    // Check if the sensor if a depth sensor
    if (rs2::depth_sensor dpt = sensor.as<rs2::depth_sensor>()) {
      return dpt.get_depth_scale();
    }
  }
  fail("Device does not have a depth sensor");
}

rs2_stream find_stream_to_align(const std::vector<rs2::stream_profile>& streams) {
  //Given a vector of streams, we try to find a depth stream and another stream to align depth with.
  //We prioritize color streams to make the view look better.
  //If color is not available, we take another stream that (other than depth)
  rs2_stream align_to = RS2_STREAM_ANY;
  bool depth_stream_found = false;
  bool color_stream_found = false;
  for (rs2::stream_profile sp : streams) {
    rs2_stream profile_stream = sp.stream_type();
    if (profile_stream == RS2_STREAM_COLOR) {
      color_stream_found = true;
      align_to = profile_stream;
    }
    if (profile_stream == RS2_STREAM_DEPTH) {
      depth_stream_found = true;
    }
  }

  if (!color_stream_found) {
    fail("no color stream available");
  }

  if (!depth_stream_found) {
    fail("no depth stream available");
  }

  return align_to;
}

const int kColorWidth = 640;
const int kColorHeight = 480;
const int kDepthWidth = 640;
const int kDepthHeight = 480;

int main(void) {
  rs2::config cfg;
  cfg.enable_stream(rs2_stream::RS2_STREAM_COLOR, 0, kColorWidth, kColorHeight, rs2_format::RS2_FORMAT_BGR8, 30);
  cfg.enable_stream(rs2_stream::RS2_STREAM_DEPTH, 0, kDepthWidth, kDepthHeight, rs2_format::RS2_FORMAT_Z16, 30);

  rs2::pipeline pipe;
  rs2::pipeline_profile profile = pipe.start(cfg);

  //rs::device * dev = ctx.get_device(0);
  //fprintf(stderr, "RealSense device opened: %s, SN %s, firmware version %s\n",
  //        dev->get_name(), dev->get_serial(), dev->get_firmware_version());
  float depth_scale = get_depth_scale(profile.get_device());
  fprintf(stderr, "Depth scale: %f\n", depth_scale);

  rs2_stream align_to = find_stream_to_align(profile.get_streams());
  rs2::align align(align_to);

  size_t color_buf_size = kColorHeight * kColorWidth * 3;
  size_t depth_buf_size = kDepthHeight * kDepthWidth * 2;

  std::vector<uint8_t> color_buf(color_buf_size);
  std::vector<uint8_t> depth_buf(depth_buf_size);

  // Skip first few frames to make sure we have a stable image.
  for (int i = 0; i < kSkipFirstFrames; i++) {
    pipe.wait_for_frames();
  }

  std::string out_prefix;
  while (1) {
    if (out_prefix == "") {
      if (!std::getline(std::cin, out_prefix)) {
        fail("Failed to read from stdin");
      }
    }
    rs2::frameset data = pipe.wait_for_frames();
    auto proccessed = align.proccess(data);
    rs2::video_frame color = proccessed.first(align_to);
    // Take the aligned depth frame.
    rs2::depth_frame depth = proccessed.get_depth_frame();

    if (!color || !depth) {
      fprintf(stderr, "Either color or depth stream is not available; will retry\n");
      continue;
    }

    if (kColorWidth != color.get_width() || kColorHeight != color.get_height()) {
      fprintf(stderr, "kColorWidth: %d. color.get_width: %d, kColorHeight: %d, color.get_height: %d\n",
              kColorWidth, color.get_width(), kColorHeight, color.get_height());
      fail("Unexpected color image resolution");
    }
    if (kDepthWidth != depth.get_width() || kDepthHeight != depth.get_height()) {
      fprintf(stderr, "kDepthWidth: %d, depth.get_width: %d, kDepthHeight: %d, depth.get_height: %d\n",
              kDepthWidth, depth.get_width(), kDepthHeight, depth.get_height());
      fail("Unexpected depth image resolution");
    }

    // Copy frames to the buffers, so we can modify the contents and be sure that
    // the new frame won't corrupt the data.
    memcpy(color_buf.data(), color.get_data(), color_buf_size);
    memcpy(depth_buf.data(), depth.get_data(), depth_buf_size);

    // Save the color frame as a JPEG image.
    cv::Mat color_mat(color.get_height(), color.get_width(), CV_8UC3, color_buf.data());
    //cv::cvtColor(color_mat, color_mat, CV_RGB2BGR);
    std::vector<int> color_params = { CV_IMWRITE_JPEG_QUALITY, 90 };
    std::string color_fname = out_prefix + "color.jpg";
    if (!cv::imwrite(color_fname, color_mat, color_params)) {
      fail("Failed to save color frame");
    }

    // Save the depth frame as a 16-bit grayscale PNG image.
    cv::Mat depth_mat(kDepthHeight, kDepthWidth, CV_16UC1, depth_buf.data());
    std::vector<int> depth_params = { CV_IMWRITE_PNG_COMPRESSION, 1 };
    std::string depth_fname = out_prefix + "depth.png";
    if (!cv::imwrite(depth_fname, depth_mat, depth_params)) {
      fail("Failed to save depth frame");
    }
    printf("OK\n");
    fflush(stdout);
    out_prefix = "";
  }
  return 0;
}
