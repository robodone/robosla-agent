cmake_minimum_required (VERSION 2.8.7)
project(realsense)

set(CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -std=c++11")

find_package(realsense REQUIRED)
find_package(OpenCV REQUIRED)
set(DEPS realsense ${OpenCV_LIBS})

add_executable(realsense-snapshot realsense-snapshot.cc)
target_link_libraries(realsense-snapshot ${DEPS})
